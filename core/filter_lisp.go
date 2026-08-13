// Lisp filter: S-expression 语法 — 函数即原语, 注册驱动。
//
//	语法:  (函数 参数...)
//	内置:  = != > >= < <= like and or not ref ref-in has-in get in subtree
//	站点:  RegisterLispFunc(name, fn) — 自定义函数（任意 AST → SQL 片段,
//	       可组合/嵌套 — 图原语、集合、业务查询都进表达式层）。
//
//	穿透:  (get 引用段... 目标字段 值) — 任意深度（递归编译, 别名唯一）
//	集合:  (in categories (subtree "slug")) — subtree 返回集合, in 包含
//
//	值: 数字 / "字符串" / true / false / {:占位符}
package core

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kran/gcm/types"
)

// ── AST ──────────────────────────────────────────

// lispExpr 表达式: 原子（值）或列表（函数调用）。
type lispExpr struct {
	atom any    // 原子值（string/float64/bool/placeholder）; 列表时 nil
	head string // 函数名（列表时）
	args []lispExpr
}

// ── 编译上下文 ───────────────────────────────────

// LispCtx 编译上下文: 注册表 + 宿主类型（穿透时变化）+ 参数/占位符。
type LispCtx struct {
	svc     *Service
	funcs   map[string]LispFunc
	td      *types.TypeDef // 当前宿主类型（get 穿透逐层变化）
	params  map[string]any
	g       phGen
	nodeRef string // 当前节点引用（"nodes" 顶层; 穿透段条件 = "t{i}"）
}

// LispFunc 函数编译器: args 是函数参数（可嵌套表达式）。
type LispFunc func(ctx *LispCtx, args []lispExpr) (string, []any, error)

// RegisterLispFunc 注册自定义 filter 函数（站点扩展; 重复注册 panic）。
func (s *Service) RegisterLispFunc(name string, fn LispFunc) {
	if _, dup := s.lispFuncs[name]; dup {
		panic(fmt.Sprintf("core: lisp func %q already registered", name))
	}
	s.lispFuncs[name] = fn
}

// ── Parser（括号递归, ~60 行）────────────────────

type lispParser struct {
	src string
	pos int
}

// parseLisp 解析 S-expression → 表达式树。
func parseLisp(src string) (lispExpr, error) {
	p := &lispParser{src: src}
	p.skipWS()
	e, err := p.parseExpr()
	if err != nil {
		return lispExpr{}, err
	}
	p.skipWS()
	if p.pos < len(p.src) {
		return lispExpr{}, fmt.Errorf("filter-lisp: unexpected trailing %q", p.src[p.pos:])
	}
	return e, nil
}

func (p *lispParser) parseExpr() (lispExpr, error) {
	p.skipWS()
	if p.pos >= len(p.src) {
		return lispExpr{}, fmt.Errorf("filter-lisp: unexpected end")
	}
	c := p.src[p.pos]
	switch {
	case c == '(':
		p.pos++ // (
		p.skipWS()
		name, err := p.parseToken()
		if err != nil {
			return lispExpr{}, err
		}
		e := lispExpr{head: name}
		for {
			p.skipWS()
			if p.pos >= len(p.src) {
				return lispExpr{}, fmt.Errorf("filter-lisp: unterminated (")
			}
			if p.src[p.pos] == ')' {
				p.pos++
				break
			}
			arg, err := p.parseExpr()
			if err != nil {
				return lispExpr{}, err
			}
			e.args = append(e.args, arg)
		}
		return e, nil
	case c == ')':
		return lispExpr{}, fmt.Errorf("filter-lisp: unexpected )")
	default:
		tok, err := p.parseToken()
		if err != nil {
			return lispExpr{}, err
		}
		return atomOf(tok), nil
	}
}

func (p *lispParser) parseToken() (string, error) {
	p.skipWS()
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ')' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", fmt.Errorf("filter-lisp: empty token at %d", p.pos)
	}
	return p.src[start:p.pos], nil
}

func (p *lispParser) skipWS() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n') {
		p.pos++
	}
}

func atomOf(tok string) lispExpr {
	if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
		return lispExpr{atom: tok[1 : len(tok)-1]}
	}
	if strings.HasPrefix(tok, "{:") && strings.HasSuffix(tok, "}") {
		return lispExpr{atom: placeholder{name: tok[2 : len(tok)-1]}}
	}
	if n, err := strconv.ParseFloat(tok, 64); err == nil {
		return lispExpr{atom: n}
	}
	if tok == "true" {
		return lispExpr{atom: true}
	}
	if tok == "false" {
		return lispExpr{atom: false}
	}
	return lispExpr{atom: tok}
}

// ── 编译入口 ─────────────────────────────────────

// CompileLisp 编译 Lisp filter 表达式 → (where 片段, args)。
// 字段校验用 typeName 的类型定义; params 是占位符绑定。
func (s *Service) CompileLisp(expr, typeName string, params map[string]any) (string, []any, error) {
	e, err := parseLisp(expr)
	if err != nil {
		return "", nil, err
	}
	td, ok := s.types.Type(typeName)
	if !ok {
		return "", nil, fmt.Errorf("core: type %q not defined", typeName)
	}
	idx := 1
	ctx := &LispCtx{svc: s, funcs: s.lispFuncs, td: &td, params: params, g: phGen{&idx}, nodeRef: "nodes"}
	if e.head == "" {
		return "", nil, fmt.Errorf("filter-lisp: top-level must be a call")
	}
	fn, ok := s.lispFuncs[e.head]
	if !ok {
		return "", nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
	return fn(ctx, e.args)
}

// lispCall 编译子表达式（注册表查找 — 递归/组合的核心）。
func (ctx *LispCtx) lispCall(e lispExpr) (string, []any, error) {
	if e.head == "" {
		return "", nil, fmt.Errorf("filter-lisp: expected call, got %v", e.atom)
	}
	fn, ok := ctx.funcs[e.head]
	if !ok {
		return "", nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
	return fn(ctx, e.args)
}

// ── 工具 ─────────────────────────────────────────

// valueOf 原子/占位符 → 参数值。
func (ctx *LispCtx) valueOf(v lispExpr) (any, error) {
	if v.head != "" {
		return nil, fmt.Errorf("filter-lisp: expected value, got (%s ...)", v.head)
	}
	return valueParam(v.atom, ctx.params)
}

// pathOf 原子路径（列/$.字段）。
func pathOf(v lispExpr) (string, error) {
	if v.head != "" {
		return "", fmt.Errorf("filter-lisp: expected path, got (%s ...)", v.head)
	}
	p, ok := v.atom.(string)
	if !ok {
		return "", fmt.Errorf("filter-lisp: path must be string")
	}
	return p, nil
}

// refTargetType 宿主类型里引用字段的目标类型。
func (ctx *LispCtx) refTargetType(field string) (string, error) {
	for _, f := range ctx.td.Fields {
		if f.Name == field && ctx.svc.types.IsRefKind(f.Kind) {
			return f.To, nil
		}
	}
	return "", fmt.Errorf("filter-lisp: %q not a ref field on %q", field, ctx.td.Name)
}
