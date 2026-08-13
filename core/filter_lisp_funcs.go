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
	// ── 引用（-> 出边 / <- 入边; <- 一元 = 入边存在性）──
	s.lispFuncs["->"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		return ctx.refCmp("->", args, false)
	}
	s.lispFuncs["<-"] = func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		if len(args) == 1 {
			// 入边存在性
			field, _ := pathOf(args[0])
			return "EXISTS(SELECT 1 FROM edges WHERE field = " + ctx.g.bind() + " AND to_node = nodes.id)", []any{field}, nil
		}
		return ctx.refCmp("<-", args, true)
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
		return "json_extract(" + ctx.nodeRef + ".fields, " + ctx.g.bind() + ") " + sqlOp(op) + " " + ctx.g.bind(),
			[]any{"$." + field, val}, nil
	}
	if types.IsNodeColumn(path) {
		return ctx.nodeRef + "." + ctx.g.quote() + " " + sqlOp(op) + " " + ctx.g.bind(),
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

// through 穿透（递归 — 任意深度, 段=表达式）。
//
//	(get (-> categories) (-> parent (= $.status 1)) $.name "根")
//
// 段表达式: (方向 字段 [条件]) — 方向 ->/<-; 字段; 可选条件（中间节点谓词）。
// 最后两参: 目标字段 + 值（目标字段可比 $. 或列）。
func (ctx *LispCtx) through(args []lispExpr) (string, []any, error) {
	if len(args) < 4 {
		return "", nil, fmt.Errorf("filter-lisp: get takes segments + target field + value")
	}
	// 最后两参: 目标字段 + 值; 前面是段表达式
	val, err := ctx.valueOf(args[len(args)-1])
	if err != nil {
		return "", nil, err
	}
	targetSeg := args[len(args)-2]
	segs := args[:len(args)-2]
	return ctx.throughRec(segs, val, targetSeg, 0, "nodes.id")
}

// throughRec 递归编译穿透: 每段一层 EXISTS, 别名唯一（e{i}/t{i}）。
// 段 = 表达式 (方向 字段 [条件]): 方向 ->/<- 全显式; 条件 = 中间节点谓词
// （如 parent 的 status=1）— 编译进该段 EXISTS 的 AND。
func (ctx *LispCtx) throughRec(segs []lispExpr, val any, targetSeg lispExpr, i int, link string) (string, []any, error) {
	var err error
	segExpr := segs[i]
	if segExpr.head == "" {
		return "", nil, fmt.Errorf("filter-lisp: through segment must be (-> field) or (<- field [cond])")
	}
	in := segExpr.head == "<-"
	if segExpr.head != "->" && segExpr.head != "<-" {
		return "", nil, fmt.Errorf("filter-lisp: through segment direction must be -> or <-")
	}
	if len(segExpr.args) < 1 {
		return "", nil, fmt.Errorf("filter-lisp: through segment needs field")
	}
	segName, _ := pathOf(segExpr.args[0])
	// 宿主切换: 本段的宿主 = 前段引用目标（段0 宿主 = ctx.td 传入值）
	if i > 0 {
		prevExpr := segs[i-1]
		prevField, _ := pathOf(prevExpr.args[0])
		to, err := ctx.refTargetType(prevField)
		if err != nil {
			return "", nil, err
		}
		htd, ok := ctx.svc.types.Type(to)
		if !ok {
			return "", nil, fmt.Errorf("filter-lisp: type %q not defined", to)
		}
		ctx.td = &htd
	}
	var condFrag string
	var condArgs []any
	if len(segExpr.args) >= 2 {
		// 中间条件属于"段目标节点" — 临时切到段目标类型
		to, terr := ctx.refTargetType(segName)
		if terr != nil {
			return "", nil, terr
		}
		htd, ok := ctx.svc.types.Type(to)
		if !ok {
			return "", nil, fmt.Errorf("filter-lisp: type %q not defined", to)
		}
		origTd, origRef := ctx.td, ctx.nodeRef
		ctx.td = &htd
		ctx.nodeRef = fmt.Sprintf("t%d", i+1)
		condFrag, condArgs, err = ctx.lispCall(segExpr.args[1])
		ctx.td, ctx.nodeRef = origTd, origRef
		if err != nil {
			return "", nil, err
		}
	}
	if _, err := ctx.refTargetType(segName); err != nil {
		return "", nil, err
	}

	var inner string
	var innerArgs []any
	if i == len(segs)-1 {
		// 最后一层: 目标字段在 t{i+1} 上
		ta := fmt.Sprintf("t%d", i+1)
		targetField, _ := pathOf(targetSeg)
		if strings.HasPrefix(targetField, "$.") {
			f2 := strings.TrimPrefix(targetField, "$.")
			inner = "json_extract(" + ta + ".fields, " + ctx.g.bind() + ") = " + ctx.g.bind()
			innerArgs = []any{"$." + f2, val}
		} else {
			inner = ta + "." + ctx.g.quote() + " = " + ctx.g.bind()
			innerArgs = []any{targetField, val}
		}
	} else {
		inner, innerArgs, err = ctx.throughRec(segs, val, targetSeg, i+1, fmt.Sprintf("t%d.id", i+1))
		if err != nil {
			return "", nil, err
		}
	}
	joinCol := "to_node"
	linkCol := "from_node"
	if in {
		joinCol = "from_node"
		linkCol = "to_node"
	}
	frag := fmt.Sprintf("EXISTS(SELECT 1 FROM edges e%d JOIN nodes t%d ON t%d.id = e%d.%s WHERE e%d.field = ", i+1, i+1, i+1, i+1, joinCol, i+1) +
		ctx.g.bind() + " AND e" + fmt.Sprintf("%d", i+1) + "." + linkCol + " = " + link
	if condFrag != "" {
		frag += " AND " + condFrag
	}
	frag += " AND " + inner + ")"
	return frag, append(append(condArgs, innerArgs...), segName), nil
}
