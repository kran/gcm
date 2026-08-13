package core

// Lisp filter 编译器（var 链构建 — dba 结构层递归）。
//
// 与手拼片段 + 全局占位符编号（phGen）不同: 每个子表达式注册为 dba 的
// var 节点（${eN} 引用, 嵌套递归）— 参数在各自 varNode 里, #{n} 从自己
// args 数 — 穿透/逻辑的参数顺序与编号由 dba 结构层管理, 不再手动对齐。
//
//	内层: q.Var("e2", `EXISTS(... WHERE field = #{1} ...)`, seg)
//	外层: q.Var("e1", `EXISTS(... WHERE field = #{1} ... ${e2})`, seg)
//	顶层: q.Add(top) → q.ToSQL() → (where, args)
//
// 构建起点 = s.db（var 注册 copy-on-write — 原库不受影响）。

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
	"github.com/kran/gcm/types"
)

// lispCompiler 编译状态: var 链 + 编号 + 宿主/节点上下文。
type lispCompiler struct {
	svc     *Service
	q       *dba.SQL
	varSeq  int
	td      *types.TypeDef
	nodeRef string // "nodes" 顶层; 穿透段 = "t{i}"
	params  map[string]any
}

// LispFuncC 函数编译器（var 槽版）: 返回片段（${eN} 引用或字面量）。
// 站点 RegisterLispFuncC 注册自定义函数（注册驱动 — 函数进表达式）。
type LispFuncC func(args []lispExpr) (string, []any, error)

// RegisterLispFuncC 注册自定义 Lisp filter 函数（站点扩展; 重复注册 panic）。
// 函数签名 func(args []lispExpr) (string, error) — 需要访问 Service/上下文时
// 用闭包捕获（注册时 svc 在作用域）。
func (s *Service) RegisterLispFuncC(name string, fn LispFuncC) {
	if _, dup := s.lispFuncsC[name]; dup {
		panic(fmt.Sprintf("core: lisp func %q already registered", name))
	}
	s.lispFuncsC[name] = fn
}

// CompileLispInto 在 q 上挂载 ${where} var（嵌套 var 同实例 — 原样保留,
// 不 ToSQL）。q 是调用方的 dba 链（base SQL 模板含 ${where} 槽）;
// 返回挂好 var 的 q, 调用方继续链式操作, 最终执行时统一展开。
func (s *Service) CompileLispInto(q *dba.SQL, expr, typeName string, params map[string]any) (*dba.SQL, error) {
	e, err := parseLisp(expr)
	if err != nil {
		return nil, err
	}
	td, ok := s.types.Type(typeName)
	if !ok {
		return nil, fmt.Errorf("core: type %q not defined", typeName)
	}
	c := &lispCompiler{svc: s, q: q, td: &td, nodeRef: "nodes", params: params}
	if e.head == "" {
		return nil, fmt.Errorf("filter-lisp: top-level must be a call")
	}
	fn, ok := c.funcs()[e.head]
	if !ok {
		return nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
	top, extraArgs, err := fn(e.args)
	if err != nil {
		return nil, err
	}
	// 顶层: ${where} 槽 — var 挂载（片段 + 顶层函数的直接参数）
	return c.q.Var("where", top, extraArgs...), nil
}

// varRef 注册片段为 ${eN}, 返回引用（参数独立编号）。
func (c *lispCompiler) varRef(frag string, args ...any) string {
	c.varSeq++
	name := fmt.Sprintf("e%d", c.varSeq)
	c.q = c.q.Var(name, frag, args...)
	return fmt.Sprintf("${%s}", name)
}

// funcs 函数表: 内置（注册驱动形态）+ 站点注册合并。
func (c *lispCompiler) funcs() map[string]LispFuncC {
	m := map[string]LispFuncC{}
	for _, op := range []string{"=", "!=", ">", ">=", "<", "<=", "like"} {
		op := op
		m[op] = wrap(func(args []lispExpr) (string, error) { return c.cmp(op, args) })
	}
	m["and"] = wrap(c.andFn)
	m["or"] = wrap(c.orFn)
	m["not"] = wrap(c.notFn)
	m["->"] = wrap(func(args []lispExpr) (string, error) { return c.refCmp(false, args) })
	m["<-"] = wrap(func(args []lispExpr) (string, error) { return c.refCmp(true, args) })
	m["get"] = wrap(c.throughFn)
	m["in"] = wrap(c.inFn)
	m["subtree"] = wrap(c.subtreeFn)
	// 站点注册函数（覆盖内置? 不 — 重复注册 panic; 合并）
	for k, v := range c.svc.lispFuncsC {
		m[k] = v
	}
	return m
}

// wrap 单值片段 → (frag, nil)（参数已挂 var; 站点函数可返回 (frag, args)）。
func wrap(fn func(args []lispExpr) (string, error)) LispFuncC {
	return func(args []lispExpr) (string, []any, error) {
		frag, err := fn(args)
		return frag, nil, err
	}
}

// sqlOp 操作符 → SQL（like 保持; 其余原样）。
func sqlOp(op string) string {
	if op == "like" {
		return "LIKE"
	}
	return op
}

// call 编译子表达式（注册表查找; 返回片段 + 直接参数）。
func (c *lispCompiler) call(e lispExpr) (string, []any, error) {
	if e.head == "" {
		return "", nil, fmt.Errorf("filter-lisp: expected call, got %v", e.atom)
	}
	fn, ok := c.funcs()[e.head]
	if !ok {
		return "", nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
	return fn(e.args)
}

// valueOf 原子/占位符 → 参数值。
func (c *lispCompiler) valueOf(v lispExpr) (any, error) {
	if v.head != "" {
		return nil, fmt.Errorf("filter-lisp: expected value, got (%s ...)", v.head)
	}
	return valueParam(v.atom, c.params)
}

func pathOfC(v lispExpr) (string, error) {
	if v.head != "" {
		return "", fmt.Errorf("filter-lisp: expected path, got (%s ...)", v.head)
	}
	p, ok := v.atom.(string)
	if !ok {
		return "", fmt.Errorf("filter-lisp: path must be string")
	}
	return p, nil
}

// refTarget 宿主类型里引用字段的目标类型。
func (c *lispCompiler) refTarget(field string) (string, error) {
	for _, f := range c.td.Fields {
		if f.Name == field && c.svc.types.IsRefKind(f.Kind) {
			return f.To, nil
		}
	}
	return "", fmt.Errorf("filter-lisp: %q not a ref field on %q", field, c.td.Name)
}

// ── 函数实现 ─────────────────────────────────────

// cmp 比较: 列/JSON（nodeRef 相对当前节点）。
func (c *lispCompiler) cmp(op string, args []lispExpr) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("filter-lisp: %s takes 2 args", op)
	}
	path, err := pathOfC(args[0])
	if err != nil {
		return "", err
	}
	val, err := c.valueOf(args[1])
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(path, "$.") {
		field := strings.TrimPrefix(path, "$.")
		if !c.isScalar(field) {
			return "", fmt.Errorf("filter-lisp: field %q not scalar on %q", field, c.td.Name)
		}
		return c.varRef("json_extract("+c.nodeRef+".fields, #{1}) "+sqlOp(op)+" #{2}", "$."+field, val), nil
	}
	if types.IsNodeColumn(path) {
		return c.varRef(c.nodeRef+".#{1|quote} "+sqlOp(op)+" #{2}", path, val), nil
	}
	return "", fmt.Errorf("filter-lisp: %q neither $.field nor column", path)
}

func (c *lispCompiler) isScalar(name string) bool {
	for _, f := range c.td.Fields {
		if f.Name == name {
			return !c.svc.types.IsRefKind(f.Kind) && f.Kind != "array" && f.Kind != "object"
		}
	}
	return false
}

// and/or/not: 组合子表达式（每个子表达式 var 注册, 引用拼接）。
func (c *lispCompiler) andFn(args []lispExpr) (string, error) { return c.logical(" AND ", args) }
func (c *lispCompiler) orFn(args []lispExpr) (string, error)  { return c.logical(" OR ", args) }
func (c *lispCompiler) notFn(args []lispExpr) (string, error) { return c.logical("NOT ", args) }
func (c *lispCompiler) logical(sep string, args []lispExpr) (string, error) {
	parts := make([]string, 0, len(args))
	if sep == "NOT " {
		if len(args) != 1 {
			return "", fmt.Errorf("filter-lisp: not takes 1 arg")
		}
		frag, _, err := c.call(args[0])
		if err != nil {
			return "", err
		}
		return c.varRef("NOT (" + frag + ")"), nil
	}
	for _, a := range args {
		frag, _, err := c.call(a)
		if err != nil {
			return "", err
		}
		parts = append(parts, frag)
	}
	return c.varRef("(" + strings.Join(parts, sep) + ")"), nil
}

// refCmp: -> 出边 / <- 入边（<- 一元 = 存在性）。
func (c *lispCompiler) refCmp(in bool, args []lispExpr) (string, error) {
	if in && len(args) == 1 {
		field, _ := pathOfC(args[0])
		return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND to_node = nodes.id)", field), nil
	}
	if len(args) != 2 {
		return "", fmt.Errorf("filter-lisp: ref takes 2 args")
	}
	field, _ := pathOfC(args[0])
	val, err := c.valueOf(args[1])
	if err != nil {
		return "", err
	}
	if in {
		return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND to_node = nodes.id AND from_node = #{2})", field, val), nil
	}
	return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND from_node = nodes.id AND to_node = #{2})", field, val), nil
}

// throughFn: 穿透（段表达式递归 — var 链嵌套）。
func (c *lispCompiler) throughFn(args []lispExpr) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("filter-lisp: get takes segments + target comparison")
	}
	segs := args
	var targetCmp lispExpr
	last := args[len(args)-1]
	if last.head == "" {
		// 原子兼容: (get 段... 字段 值)
		if len(args) < 3 {
			return "", fmt.Errorf("filter-lisp: get needs target comparison")
		}
		val, err := c.valueOf(args[len(args)-1])
		if err != nil {
			return "", err
		}
		targetCmp = lispExpr{head: "=", args: []lispExpr{args[len(args)-2], {atom: val}}}
		segs = args[:len(args)-2]
	} else {
		targetCmp = last
		segs = args[:len(args)-1]
	}
	return c.throughRec(segs, targetCmp, 0, "nodes.id")
}

// throughRec: 每段一层 EXISTS var; 段条件/目标比较 = 内嵌 var 引用。
func (c *lispCompiler) throughRec(segs []lispExpr, targetCmp lispExpr, i int, link string) (string, error) {
	segExpr := segs[i]
	if segExpr.head == "" {
		return "", fmt.Errorf("filter-lisp: through segment must be (-> field) or (<- field [cond])")
	}
	in := segExpr.head == "<-"
	if segExpr.head != "->" && segExpr.head != "<-" {
		return "", fmt.Errorf("filter-lisp: through segment direction must be -> or <-")
	}
	segName, _ := pathOfC(segExpr.args[0])

	// 宿主切换: 本段宿主 = 前段引用目标（段0 = c.td）
	if i > 0 {
		prev, _ := pathOfC(segs[i-1].args[0])
		to, err := c.refTarget(prev)
		if err != nil {
			return "", err
		}
		htd, ok := c.svc.types.Type(to)
		if !ok {
			return "", fmt.Errorf("filter-lisp: type %q not defined", to)
		}
		c.td = &htd
	}
	if _, err := c.refTarget(segName); err != nil {
		return "", err
	}
	// 段目标类型（条件/末比较的宿主）
	to, err := c.refTarget(segName)
	if err != nil {
		return "", err
	}
	htd, ok := c.svc.types.Type(to)
	if !ok {
		return "", fmt.Errorf("filter-lisp: type %q not defined", to)
	}

	joinCol, linkCol := "to_node", "from_node"
	if in {
		joinCol, linkCol = "from_node", "to_node"
	}

	// 中间条件（段表达式第 2 参）
	condRef := ""
	if len(segExpr.args) >= 2 {
		origTd, origRef := c.td, c.nodeRef
		c.td = &htd
		c.nodeRef = fmt.Sprintf("t%d", i+1)
		frag, _, err := c.call(segExpr.args[1])
		c.td, c.nodeRef = origTd, origRef
		if err != nil {
			return "", err
		}
		condRef = frag // 已是 ${eN} 引用
	}

	// 递归下一段或目标比较
	var inner string
	if i == len(segs)-1 {
		origTd, origRef := c.td, c.nodeRef
		c.td = &htd
		c.nodeRef = fmt.Sprintf("t%d", i+1)
		frag, _, err := c.call(targetCmp)
		c.td, c.nodeRef = origTd, origRef
		if err != nil {
			return "", err
		}
		inner = frag
	} else {
		var err error
		inner, err = c.throughRec(segs, targetCmp, i+1, fmt.Sprintf("t%d.id", i+1))
		if err != nil {
			return "", err
		}
	}
	// 本段 EXISTS var: 参数只有字段名（条件/内层是 ${eN} 引用 — 参数在各自 var）
	frag := fmt.Sprintf("EXISTS(SELECT 1 FROM edges e%d JOIN nodes t%d ON t%d.id = e%d.%s WHERE e%d.field = #{1} AND e%d.%s = %s", i+1, i+1, i+1, i+1, joinCol, i+1, i+1, linkCol, link)
	if condRef != "" {
		frag += " AND " + condRef
	}
	frag += " AND " + inner + ")"
	return c.varRef(frag, segName), nil
}

// inFn: (in 字段 (subtree "slug")) — 集合函数返回 id 列表参数。
func (c *lispCompiler) inFn(args []lispExpr) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("filter-lisp: in takes 2 args")
	}
	field, _ := pathOfC(args[0])
	// 集合函数（subtree）注册一个"参数槽" var — 值 = id 列表
	// 设计: 集合函数返回 ${eN}（var 带 id 列表参数）; in 引用它并拼 expand
	setRef, setArgs, err := c.call(args[1])
	if err != nil {
		return "", err
	}
	// 集合函数协议: 返回 id 切片（一个参数）; in 用 expand 展开。
	// setRef 是 ${eN} 引用（subtree — var 带 id 切片参数）或字面量（自定义函数）。
	// setArgs 是自定义函数的直接参数（含 id 切片）— 并入 in 的 var。
	// subtree: setArgs 为 nil（id 切片已在 var 的 args 里）— in 引用 ${eN}。
	// 统一: setArgs 若有, 追加到 field 后; expand 用 #{2|expand}。
	if len(setArgs) > 0 {
		// 自定义函数直接参数（id 切片）— 拼 expand
		allArgs := append([]any{field}, setArgs...)
		return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND from_node = nodes.id AND to_node IN (#{2|expand}))", allArgs...), nil
	}
	// subtree: ${eN} 引用（其 var 含 id 切片参数）— 直接引用
	return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND from_node = nodes.id AND to_node IN ("+setRef+"))", field), nil
}

// subtreeFn: (subtree "slug") — 返回 id 列表（参数传给 in 的 expand）。
func (c *lispCompiler) subtreeFn(args []lispExpr) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("filter-lisp: subtree takes 1 arg")
	}
	slug, _ := pathOfC(args[0])
	cat, err := c.svc.GetBySlug(slug)
	if err != nil || cat == nil {
		return "", fmt.Errorf("filter-lisp: subtree %q not found", slug)
	}
	ids, err := c.svc.Subtree(cat.ID, "parent", 20)
	if err != nil {
		return "", err
	}
	ids = append([]int64{cat.ID}, ids...)
	anyIDs := make([]any, len(ids))
	for i, id := range ids {
		anyIDs[i] = id
	}
	// 返回 ${eN} 引用（var 片段 = #{1|expand}, 参数 = id 切片 —
	// in 引用展开后是 expand 占位, 参数用 subtree 自己的）
	return c.varRef("#{1|expand}", anyIDs), nil
}
