// Package tpl 通用模板引擎 — html/template 的薄封装, 零依赖、无缓存。
//
// 职责只有三件:
//   - 级联解析: 候选文件名列表 → 找第一个存在的文件
//   - 片段:     partial / partialOr (带兜底) — 页面结构由各模板自行
//     引入 ({{ partial "header.html" . }}), 无隐式布局
//   - 函数注入: 每引擎独立 FuncMap — 查询函数定义在 Go 层, 注入后
//     模板一行调用 (模板作者不写 SQL, 查询一致性由函数库保证)
//
// 无缓存设计 (第一期): 每次渲染读文件+解析, 天然热重载 (改文件下一请求
// 生效), 无共享可变状态故并发安全; 解析失败每次请求响亮 500 (失败响亮)。
// 流量显示需要时, 在 Render 签名后加缓存层即可, 调用方零改动。
package tpl

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Masterminds/sprig/v3"
	"jaytaylor.com/html2text"
)

// Engine 一个模板引擎实例 (一般对应一个站点)。
type Engine struct {
	mu    sync.RWMutex
	root  string
	funcs template.FuncMap
}

// New 创建引擎; funcs 为初始自定义函数 (可后续 Func 追加)。
func New(root string, funcs ...template.FuncMap) *Engine {
	e := &Engine{root: root, funcs: template.FuncMap{}}
	for _, m := range funcs {
		for k, v := range m {
			e.funcs[k] = v
		}
	}
	return e
}

// Func 注册自定义模板函数。
func (e *Engine) Func(name string, fn any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.funcs[name] = fn
}

// Render 按候选序取第一个存在的模板执行 (级联)。数据任意形状。
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
	return fmt.Errorf("tpl: no template for %q (candidates: %s)", e.root, strings.Join(candidates, ", "))
}

// execute 单个模板文件独立解析执行 (无隐式布局 — 页面结构由模板自行
// 经 partial 引入)。
func (e *Engine) execute(w io.Writer, name, full string, data any) error {
	tpl, err := template.New(filepath.Base(full)).Funcs(e.funcMap()).ParseFiles(full)
	if err != nil {
		return fmt.Errorf("tpl: parse %s: %w", name, err)
	}
	return tpl.Execute(w, data)
}

// Partial 渲染片段模板 (独立解析执行; 不参与布局)。
// 无缓存, 每次渲染读当前文件 — 片段也热重载。
func (e *Engine) Partial(name string, data any) (template.HTML, error) {
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("tpl: partial %q escapes templates root", name)
	}
	full := filepath.Join(e.root, clean)
	if _, err := os.Stat(full); err != nil {
		return "", errPartialNotFound
	}
	tpl, err := template.New(filepath.Base(full)).Funcs(e.funcMap()).ParseFiles(full)
	if err != nil {
		return "", fmt.Errorf("tpl: parse partial %s: %w", name, err)
	}
	var sb strings.Builder
	if err := tpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("tpl: partial %q: %w", name, err)
	}
	return template.HTML(sb.String()), nil
}

// errPartialNotFound 片段缺失哨兵: partialOr 据此回落兜底。
var errPartialNotFound = errors.New("tpl: partial not found")

// funcMap 内置函数 + sprig (安全子集) + 自定义函数。
// sprig: HermeticHtmlFuncMap — 无 env/expandenv 等环境访问的安全子集
// (dict/default/trunc 等常用模板工具函数)。
// safeHTML: 受信富文本原样输出 (匿名提交内容禁用, XSS)。
func (e *Engine) funcMap() template.FuncMap {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m := template.FuncMap{}
	for k, v := range sprig.HermeticHtmlFuncMap() {
		m[k] = v
	}
	builtins := template.FuncMap{
		"safeHTML": func(v any) template.HTML { return template.HTML(fmt.Sprint(v)) },
		"partial": func(name string, data any) (template.HTML, error) {
			return e.Partial(name, data)
		},
		"partialOr": func(name, fallback string, data any) (template.HTML, error) {
			out, err := e.Partial(name, data)
			if err != nil {
				if errors.Is(err, errPartialNotFound) {
					return e.Partial(fallback, data)
				}
				return "", err // 解析/执行错误响亮上抛, 不吞进兜底
			}
			return out, nil
		},
		// HTML → 纯文本 (列表/首页缩略): 剥标签 + 块元素转换行
		"plainText": func(html string) string {
			if html == "" {
				return ""
			}
			text, err := html2text.FromString(html)
			if err != nil {
				return strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " "))
			}
			return strings.TrimSpace(text)
		},
	}
	for k, v := range builtins { // 内置覆盖 sprig 同名 (如有)
		m[k] = v
	}
	for k, v := range e.funcs {
		m[k] = v
	}
	return m
}
