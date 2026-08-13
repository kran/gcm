package core

// Lisp filter 内置函数（注册驱动 — 每个函数一个注册项, 与站点扩展同构）。

import (
	"fmt"
	"strings"

	"github.com/kran/gcm/types"
)

// registerBuiltinLispFuncs 注册内置函数（New 时调用）。
func (s *Service) registerBuiltinLispFuncs() {
	// ── 比较（列/JSON）──────────────────────────
	for _, op := range []string{"=", "!=", ">", ">=", "<", "<=", "like"} {
		op := op
		s.lispFuncs[op] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
			if len(args) != 2 {
				return "", nil, fmt.Errorf("filter-lisp: %s takes 2 args", op)
			}
			path, err := pathOf(args[0])
			if err != nil {
				return "", nil, err
			}
			val, err := ctx.valueOf(args[1])
			if err != nil {
				return "", nil, err
			}
			return ctx.cmp(op, path, val)
		}
	}
	// ── 逻辑 ──────────────────────────────────
	s.lispFuncs["and"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		return ctx.logical(" AND ", args)
	}
	s.lispFuncs["or"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		return ctx.logical(" OR ", args)
	}
	s.lispFuncs["not"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		if len(args) != 1 {
			return "", nil, fmt.Errorf("filter-lisp: not takes 1 arg")
		}
		frag, a, err := ctx.lispCall(args[0])
		if err != nil {
			return "", nil, err
		}
		return "NOT (" + frag + ")", a, nil
	}
	// ── 引用 ──────────────────────────────────
	s.lispFuncs["ref"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		return ctx.refCmp("ref", args, false)
	}
	s.lispFuncs["ref-in"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		return ctx.refCmp("ref-in", args, true)
	}
	s.lispFuncs["has-in"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		if len(args) != 1 {
			return "", nil, fmt.Errorf("filter-lisp: has-in takes 1 arg")
		}
		field, _ := pathOf(args[0])
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + ctx.g.bind() + " AND to_node = nodes.id)", []any{field}, nil
	}
	// ── 穿透（递归 — 函数内部调 lispCall 组合）─────
	s.lispFuncs["get"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		return ctx.through(args)
	}
	// ── 集合 ──────────────────────────────────
	s.lispFuncs["in"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		if len(args) != 2 {
			return "", nil, fmt.Errorf("filter-lisp: in takes 2 args")
		}
		field, _ := pathOf(args[0])
		// 集合表达式（subtree 等注册函数）: 返回 id 列表
		setFrag, setArgs, err := ctx.lispCall(args[1])
		if err != nil {
			return "", nil, err
		}
		_ = setFrag
		// 集合函数返回 []any 值（如 subtree 的 id 列表）— 用 expand
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + ctx.g.bind() +
				" AND from_node = nodes.id AND to_node IN (" + ctx.g.expand() + "))",
			append([]any{field}, setArgs...), nil
	}
	// ── 图原语（集合函数 — 供 in 用）─────────────
	s.lispFuncs["subtree"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		if len(args) != 1 {
			return "", nil, fmt.Errorf("filter-lisp: subtree takes 1 arg")
		}
		slug, err := pathOf(args[0])
		if err != nil {
			return "", nil, err
		}
		cat, err := ctx.svc.GetBySlug(slug)
		if err != nil || cat == nil {
			return "", nil, fmt.Errorf("filter-lisp: subtree %q not found", slug)
		}
		ids, err := ctx.svc.Subtree(cat.ID, "parent", 20)
		if err != nil {
			return "", nil, err
		}
		ids = append([]int64{cat.ID}, ids...)
		anyIDs := make([]any, len(ids))
		for i, id := range ids {
			anyIDs[i] = id
		}
		// 返回值: 空片段 + id 列表**包一层**（in 用 expand 展开整个切片）
		return "", []any{anyIDs}, nil
	}
}

// cmp 比较（列/JSON 路径）。
func (ctx *LispCtx) cmp(op, path string, val any) (string, []any, error) {
	if strings.HasPrefix(path, "$.") {
		field := strings.TrimPrefix(path, "$.")
		if !ctx.isScalar(field) {
			return "", nil, fmt.Errorf("filter-lisp: field %q not scalar on %q", field, ctx.td.Name)
		}
		if op == "~" {
			return "", nil, fmt.Errorf("filter-lisp: ~ not for JSON")
		}
		return "json_extract(nodes.fields, " + ctx.g.bind() + ") " + sqlOp(op) + " " + ctx.g.bind(),
			[]any{"$." + field, val}, nil
	}
	if types.IsNodeColumn(path) {
		return "nodes." + ctx.g.quote() + " " + sqlOp(op) + " " + ctx.g.bind(),
			[]any{path, val}, nil
	}
	return "", nil, fmt.Errorf("filter-lisp: %q neither $.field nor column", path)
}

func (ctx *LispCtx) isScalar(name string) bool {
	for _, f := range ctx.td.Fields {
		if f.Name == name {
			return !ctx.svc.types.IsRefKind(f.Kind) && f.Kind != "array" && f.Kind != "object"
		}
	}
	return false
}

// logical AND/OR 组合。
func (ctx *LispCtx) logical(op string, args []lispExpr) (string, []any, error) {
	parts := make([]string, 0, len(args))
	var out []any
	for _, a := range args {
		frag, aa, err := ctx.lispCall(a)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, frag)
		out = append(out, aa...)
	}
	return "(" + strings.Join(parts, op) + ")", out, nil
}

// refCmp 引用比较（出/入边）。
func (ctx *LispCtx) refCmp(name string, args []lispExpr, in bool) (string, []any, error) {
	if len(args) != 2 {
		return "", nil, fmt.Errorf("filter-lisp: %s takes 2 args", name)
	}
	field, _ := pathOf(args[0])
	val, err := ctx.valueOf(args[1])
	if err != nil {
		return "", nil, err
	}
	if in {
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + ctx.g.bind() + " AND to_node = nodes.id AND from_node = " + ctx.g.bind() + ")",
			[]any{field, val}, nil
	}
	return "EXISTS(SELECT 1 FROM edges WHERE field = " + ctx.g.bind() + " AND from_node = nodes.id AND to_node = " + ctx.g.bind() + ")",
		[]any{field, val}, nil
}

// through 穿透（递归 — 任意深度）。
func (ctx *LispCtx) through(args []lispExpr) (string, []any, error) {
	if len(args) < 3 {
		return "", nil, fmt.Errorf("filter-lisp: get takes path + value")
	}
	val, err := ctx.valueOf(args[len(args)-1])
	if err != nil {
		return "", nil, err
	}
	segs := args[:len(args)-1]
	return ctx.throughRec(segs, val, 0, "nodes.id")
}

// throughRec 递归编译穿透: 每段一层 EXISTS, 别名唯一（e{i}/t{i}）。
func (ctx *LispCtx) throughRec(segs []lispExpr, val any, i int, link string) (string, []any, error) {
	segName, _ := pathOf(segs[i])
	// 当前层宿主类型: 段0 = ctx.td; 更深层 = 前段的 ref 目标
	if i > 0 {
		prev, _ := pathOf(segs[i-1])
		to, err := ctx.refTargetType(prev)
		if err != nil {
			return "", nil, err
		}
		htd, ok := ctx.svc.types.Type(to)
		if !ok {
			return "", nil, fmt.Errorf("filter-lisp: type %q not defined", to)
		}
		ctx.td = &htd
	}
	to, err := ctx.refTargetType(segName)
	if err != nil {
		return "", nil, err
	}
	// 下一层宿主类型 = 本段目标（递归时用）
	nextCtx := *ctx
	if i == 0 {
		// 保存原始 ctx（递归恢复用 — 简化: 每层重查类型）
	}
	_ = to
	_ = nextCtx

	var inner string
	var innerArgs []any
	if i == len(segs)-2 {
		// 目标字段（在 t{i+1} 上）
		ta := fmt.Sprintf("t%d", i+1)
		targetField, _ := pathOf(segs[i+1])
		if strings.HasPrefix(targetField, "$.") {
			f2 := strings.TrimPrefix(targetField, "$.")
			inner = "json_extract(" + ta + ".fields, " + ctx.g.bind() + ") = " + ctx.g.bind()
			innerArgs = []any{"$." + f2, val}
		} else {
			inner = ta + "." + ctx.g.quote() + " = " + ctx.g.bind()
			innerArgs = []any{targetField, val}
		}
	} else {
		inner, innerArgs, err = ctx.throughRec(segs, val, i+1, fmt.Sprintf("t%d.id", i+1))
		if err != nil {
			return "", nil, err
		}
	}
	frag := fmt.Sprintf("EXISTS(SELECT 1 FROM edges e%d JOIN nodes t%d ON t%d.id = e%d.to_node WHERE e%d.field = ", i+1, i+1, i+1, i+1, i+1) +
		ctx.g.bind() + " AND e" + fmt.Sprintf("%d", i+1) + ".from_node = " + link + " AND " + inner + ")"
	return frag, append(innerArgs, segName), nil
}
