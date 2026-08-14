// Package render 站点渲染引擎 — 模板引擎（级联/片段/sprig）+ 查询函数注入。
//
// 查询函数定义在 Go 层（引擎原语 + dba）, 注入 funcMap 后模板一行调用:
//
//	{{ $arts := outRefs 5 "authors" 1 10 }}{{ range $arts }}...{{ end }}
//
// 模板作者不写 SQL。查询错误 fail-loud: panic 传播为渲染错误
// （html/template 捕获 panic 作为 Execute 错误, 渲染层统一处理）。
//
// 无缓存设计: 每次渲染读文件+解析, 天然热重载（改文件下一请求生效）;
// 解析失败每次请求响亮 500（失败响亮）。
package render

import (
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kran/gcm/core"
	"github.com/kran/gcm/types"
)

// Engine 渲染引擎: 模板根目录 + 函数表 + 核心服务（查询注入源）。
type Engine struct {
	mu    sync.RWMutex
	root  string
	funcs template.FuncMap // 自定义函数（查询函数 + 站点业务函数）
	core  *core.Service
}

// New 建渲染引擎。root 是模板目录; svc 提供查询函数。
func New(root string, svc *core.Service) *Engine {
	e := &Engine{root: root, core: svc, funcs: template.FuncMap{}}
	for k, v := range e.queryFuncs() {
		e.funcs[k] = v
	}
	return e
}

// Func 注册自定义模板函数（站点项目扩展, 如业务查询）。
func (e *Engine) Func(name string, fn any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.funcs[name] = fn
}

// Render 按候选序取第一个存在的模板执行（级联: node--{type}.html → node.html）。
func (e *Engine) Render(w io.Writer, candidates []string, data any) error {
	for _, name := range candidates {
		full := filepath.Join(e.root, name)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		if err := e.execute(w, name, full, data); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("render: no template for %q (candidates: %s)", e.root, strings.Join(candidates, ", "))
}

// Candidates 节点级联候选名:
// node--{type}--{slug}.html → node--{type}.html → node.html（viicn 专属页模式）。
// slug 白名单校验: 非法 slug（含 / . 等）不进文件名 — 候选名经 filepath.Join
// 拼到模板根, 防路径穿越; 非法时回落类型级。
func Candidates(n *core.Node) []string {
	if n != nil && n.Type != "" {
		if n.Slug != "" && safeSlug(n.Slug) {
			return []string{"node--" + n.Type + "--" + n.Slug + ".html", "node--" + n.Type + ".html", "node.html"}
		}
		return []string{"node--" + n.Type + ".html", "node.html"}
	}
	return []string{"node.html"}
}

// safeSlug slug 是否 URL/文件名安全 — 与写入期约束统一（types.ValidSlug）。
func safeSlug(s string) bool { return types.ValidSlug(s) }

// fail 查询错误 → panic（html/template 捕获为 Execute 错误, fail-loud）。
func fail(err error) {
	if err != nil {
		panic(err)
	}
}

// queryFuncs 查询原语（Go 层实现）+ 展示工具。
// 返回 1 值 — 模板一行调用; 错误走 panic。模板执行时经 funcMap()
// （tpl.go）合并 sprig + 内置函数后整体注入。
func (e *Engine) queryFuncs() template.FuncMap {
	svc := e.core
	return template.FuncMap{
		// ── 查询原语 ─────────────────────────
		// get: 单节点（id 兼容 JSON float64 / int64）
		"get": func(id any) *core.Node {
			nid, err := core.ToID(id)
			fail(err)
			n, err := svc.GetNodeById(nid)
			fail(err)
			return n
		},
		// list: 按类型列表（status<0 不过滤 — Lisp 合成）
		"list": func(typ string, status, page, size int) []core.Node {
			f := `(= type "` + typ + `")`
			if status >= 0 {
				f = fmt.Sprintf(`(and (= type "%s") (= status %d))`, typ, status)
			}
			list, _, err := svc.Q(core.ListQuery{Filter: f, Page: page, Size: size})
			fail(err)
			return list
		},
		// setting: 站点配置值（按 key 取; 缺失 → nil; 值按 JSON 形态,
		// richtext 模板自行 safeHTML）
		"setting": func(key string) any {
			st, err := svc.GetSetting(key)
			fail(err)
			if st == nil {
				return nil
			}
			return st.Value
		},
		// search: 全文检索（FTS5+bigram; 只索引 search:true 类型的已发布节点）
		"search": func(q, typ string, page, size int) []core.Node {
			list, _, err := svc.Search(q, typ, page, size)
			fail(err)
			return list
		},
		// outRefs: 出边目标节点列表（symmetric 双向）
		"outRefs": func(from int64, field string, page, size int) []core.Node {
			return e.targets(true, func() ([]core.Edge, int64, error) {
				n, err := svc.GetNodeById(from)
				if err != nil || n == nil {
					return nil, 0, fmt.Errorf("outRefs: node %d not found", from)
				}
				return svc.OutEdges(n.Type, from, field, page, size)
			})
		},
		// inRefs: 入边来源节点列表（inverse 反向 — 取 from_node 端）
		"inRefs": func(to int64, field string, page, size int) []core.Node {
			return e.targets(false, func() ([]core.Edge, int64, error) {
				return svc.InEdges(to, field, page, size)
			})
		},
		// traverse: 出边递归（祖先链）
		"traverse": func(start int64, field string, maxHops int) []int64 {
			return e.graph(start, field, maxHops, svc.Traverse)
		},
		// subtree: 入边递归（子树）
		"subtree": func(start int64, field string, maxHops int) []int64 {
			return e.graph(start, field, maxHops, svc.Subtree)
		},
		// equivalence: 等价类
		"equivalence": func(start int64, field string, maxHops int) []int64 {
			return e.graph(start, field, maxHops, svc.EquivalenceClass)
		},
		// filterList: Lisp filter 筛选列表（表达式 + 分页）。
		// 用法: {{ filterList "article" "(and (= status 1) (in categories (subtree {:slug})))" (dict "slug" "x") 1 10 }}
		"filterList": func(typ, expr string, params map[string]any, page, size int) []core.Node {
			// typ 合成进 filter（(= type "x")）— 类型过滤由使用方构建
			f := expr
			if typ != "" {
				f = `(and (= type "` + typ + `") ` + expr + `)`
			}
			list, _, err := e.core.Q(core.ListQuery{Filter: f, Page: page, Size: size}, params)
			fail(err)
			return list
		},
		// expand: 统一路径展开 — 输入任意形态（单值或列表）, 返回 any。
		// 用法（管道: 数据是末参）:
		//   {{ $n := get 5 | expand "authors, categories" }}  → *Node（带 Expand）
		//   {{ $arts | expand "authors" }}                   → []*Node（批量, 查询次数与列表大小无关）
		// 输入: *Node / Node / int64 / int / float64 / []Node / []*Node / []int64 / []int / []any(id)
		"expand": func(expr string, v any) any {
			switch t := v.(type) {
			case *core.Node:
				n, err := svc.ExpandPath(t.ID, expr)
				fail(err)
				return n
			case core.Node:
				n, err := svc.ExpandPath(t.ID, expr)
				fail(err)
				return n
			case int64, int, float64:
				nid, err := core.ToID(v)
				fail(err)
				n, err := svc.ExpandPath(nid, expr)
				fail(err)
				return n
			case []core.Node:
				expanded, err := svc.ExpandPathMany(nodeIDs(t), expr)
				fail(err)
				return expanded
			case []*core.Node:
				ids := make([]int64, 0, len(t))
				for _, n := range t {
					if n != nil {
						ids = append(ids, n.ID)
					}
				}
				expanded, err := svc.ExpandPathMany(ids, expr)
				fail(err)
				return expanded
			case []int64:
				expanded, err := svc.ExpandPathMany(t, expr)
				fail(err)
				return expanded
			case []int:
				ids := make([]int64, 0, len(t))
				for _, id := range t {
					ids = append(ids, int64(id))
				}
				expanded, err := svc.ExpandPathMany(ids, expr)
				fail(err)
				return expanded
			case []any:
				ids := make([]int64, 0, len(t))
				for _, item := range t {
					nid, err := core.ToID(item)
					fail(err)
					ids = append(ids, nid)
				}
				expanded, err := svc.ExpandPathMany(ids, expr)
				fail(err)
				return expanded
			default:
				fail(fmt.Errorf("expand: unsupported input %T (want Node / id / list of them)", v))
				return nil
			}
		},

		// ── 展示工具 ─────────────────────────
		// datefmt: 时间格式化（Node.CreatedAt 等）
		"datefmt": func(t time.Time, layout string) string {
			return t.Format(layout)
		},
	}
}

// nodeIDs 节点切片 → id 列表。
func nodeIDs(nodes []core.Node) []int64 {
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

// targets 边 → 端点节点列表（保持边序; N+1 顶着, 页面量小毫秒级）。
// wantTo: 取 to_node（出边目标）; false 取 from_node（入边来源）。
// graph 模板函数桥: 查节点类型（模板场景只有 id）后转发图原语。
func (e *Engine) graph(start int64, field string, maxHops int, fn func(string, int64, string, int) ([]int64, error)) []int64 {
	n, err := e.core.GetNodeById(start)
	if err != nil || n == nil {
		fail(fmt.Errorf("graph: node %d not found", start))
		return nil
	}
	ids, err := fn(n.Type, start, field, maxHops)
	fail(err)
	return ids
}

func (e *Engine) targets(wantTo bool, q func() ([]core.Edge, int64, error)) []core.Node {
	edges, _, err := q()
	fail(err)
	nodes := make([]core.Node, 0, len(edges))
	for _, ed := range edges {
		id := ed.FromNode
		if wantTo {
			id = ed.ToNode
		}
		n, err := e.core.GetNodeById(id)
		fail(err)
		if n != nil {
			nodes = append(nodes, *n)
		}
	}
	return nodes
}
