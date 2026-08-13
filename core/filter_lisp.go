package core

// Lisp filter 原型: S-expression 语法 — 解析极简（括号递归）、递归天然、
// 函数即原语（subtree/chain 内嵌 — 图原语下沉到表达式层）。
//
//	语法:  (函数 参数...)
//	比较:  (= status 1) (>= $.views 10) (like $.title "x")
//	引用:  (ref categories 5)          — 出边 ~
//	        (ref-in authors 5)         — 入边 <-
//	        (has-in authors)           — 入边存在性
//	穿透:  (get authors level "senior")  — 任意深度（路径即参数）
//	集合:  (in categories (subtree "current"))  — 图原语内嵌
//	逻辑:  (and ...) (or ...) (not ...)
//
// 值: 数字 / "字符串" / true / false / {:占位符}

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

// ── Parser（括号递归, ~50 行）────────────────────

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
		// 列表: (函数 参数...)
		p.pos++ // (
		p.skipWS()
		// 第一个 token 是函数名
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
		// 原子: 数字/字符串/bool/占位符/标识符
		tok, err := p.parseToken()
		if err != nil {
			return lispExpr{}, err
		}
		return atomOf(tok), nil
	}
}

// parseToken 读一个 token（到空白或括号为止）。
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

// atomOf token → 原子值（数字/字符串/bool/占位符/裸标识符）。
func atomOf(tok string) lispExpr {
	// 字符串
	if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
		return lispExpr{atom: tok[1 : len(tok)-1]}
	}
	// 占位符 {:name}
	if strings.HasPrefix(tok, "{:") && strings.HasSuffix(tok, "}") {
		return lispExpr{atom: placeholder{name: tok[2 : len(tok)-1]}}
	}
	// 数字
	if n, err := strconv.ParseFloat(tok, 64); err == nil {
		return lispExpr{atom: n}
	}
	// bool
	if tok == "true" {
		return lispExpr{atom: true}
	}
	if tok == "false" {
		return lispExpr{atom: false}
	}
	// 裸标识符（列名/$.字段）
	return lispExpr{atom: tok}
}

// ── 编译（Lisp AST → 参数化 WHERE 片段）─────────────
//
// 与中缀编译共用 phGen（占位符生成器）; 函数名 → SQL 片段。
// 图原语（subtree/chain）内嵌: 编译时 Go 层算集合 → IN 参数。

// compileLisp 编译 Lisp 表达式 → (where 片段, args)。
func (s *Service) compileLisp(e lispExpr, td *types.TypeDef, params map[string]any, g phGen) (string, []any, error) {
	if e.head == "" {
		// 原子不能作为顶层表达式
		return "", nil, fmt.Errorf("filter-lisp: top-level must be a call, got %v", e.atom)
	}
	switch e.head {
	case "and", "or":
		parts := make([]string, 0, len(e.args))
		var args []any
		for _, a := range e.args {
			frag, aa, err := s.compileLisp(a, td, params, g)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, frag)
			args = append(args, aa...)
		}
		op := " AND "
		if e.head == "or" {
			op = " OR "
		}
		return "(" + strings.Join(parts, op) + ")", args, nil
	case "not":
		if len(e.args) != 1 {
			return "", nil, fmt.Errorf("filter-lisp: not takes 1 arg")
		}
		frag, args, err := s.compileLisp(e.args[0], td, params, g)
		if err != nil {
			return "", nil, err
		}
		return "NOT (" + frag + ")", args, nil
	case "=", "!=", ">", ">=", "<", "<=", "like":
		return s.lispCmp(e, td, params, g)
	case "ref", "ref-in", "has-in", "get", "in":
		return s.lispRef(e, td, params, g)
	default:
		return "", nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
}

// lispCmp 比较: (= 路径 值)。
func (s *Service) lispCmp(e lispExpr, td *types.TypeDef, params map[string]any, g phGen) (string, []any, error) {
	if len(e.args) != 2 {
		return "", nil, fmt.Errorf("filter-lisp: %s takes 2 args", e.head)
	}
	path, ok := e.args[0].atom.(string)
	if !ok {
		return "", nil, fmt.Errorf("filter-lisp: %s first arg must be path", e.head)
	}
	val, err := valueParam(e.args[1].atom, params)
	if err != nil {
		return "", nil, err
	}
	// 解析路径: $.字段（JSON）或 列
	if strings.HasPrefix(path, "$.") {
		field := strings.TrimPrefix(path, "$.")
		if !s.fieldIsScalar(*td, field) {
			return "", nil, fmt.Errorf("filter-lisp: field %q not scalar on %q", field, td.Name)
		}
		if e.head == "~" {
			return "", nil, fmt.Errorf("filter-lisp: ~ not for JSON")
		}
		return "json_extract(nodes.fields, " + g.bind() + ") " + sqlOp(e.head) + " " + g.bind(),
			[]any{"$." + field, val}, nil
	}
	// 列
	if types.IsNodeColumn(path) {
		if e.head == "~" {
			return "", nil, fmt.Errorf("filter-lisp: ~ not for column")
		}
		return "nodes." + g.quote() + " " + sqlOp(e.head) + " " + g.bind(),
			[]any{path, val}, nil
	}
	return "", nil, fmt.Errorf("filter-lisp: %q neither $.field nor column", path)
}

// fieldIsScalar 字段是标量（非 ref/复合）。
func (s *Service) fieldIsScalar(td types.TypeDef, name string) bool {
	for _, f := range td.Fields {
		if f.Name == name {
			return !s.types.IsRefKind(f.Kind) && f.Kind != "array" && f.Kind != "object"
		}
	}
	return false
}

// lispRef 引用/集合/穿透: (ref field value) (ref-in field value) (has-in field)
// (get path... value) (in field (subtree "slug"))。
func (s *Service) lispRef(e lispExpr, td *types.TypeDef, params map[string]any, g phGen) (string, []any, error) {
	switch e.head {
	case "ref", "ref-in":
		if len(e.args) != 2 {
			return "", nil, fmt.Errorf("filter-lisp: %s takes 2 args", e.head)
		}
		field, ok := e.args[0].atom.(string)
		if !ok {
			return "", nil, fmt.Errorf("filter-lisp: %s first arg must be field", e.head)
		}
		val, err := valueParam(e.args[1].atom, params)
		if err != nil {
			return "", nil, err
		}
		in := e.head == "ref-in"
		if in {
			return "EXISTS(SELECT 1 FROM edges WHERE field = " + g.bind() + " AND to_node = nodes.id AND from_node = " + g.bind() + ")", []any{field, val}, nil
		}
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + g.bind() + " AND from_node = nodes.id AND to_node = " + g.bind() + ")", []any{field, val}, nil
	case "has-in":
		if len(e.args) != 1 {
			return "", nil, fmt.Errorf("filter-lisp: has-in takes 1 arg")
		}
		field, _ := e.args[0].atom.(string)
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + g.bind() + " AND to_node = nodes.id)", []any{field}, nil
	case "get":
		// 穿透（递归）: (get 引用段... 目标字段 值) — 每段一层引用 EXISTS,
		// 最后一段是目标字段（$.x JSON 或列）; 多层 = 嵌套 EXISTS。
		if len(e.args) < 3 {
			return "", nil, fmt.Errorf("filter-lisp: get takes path + value")
		}
		val, err := valueParam(e.args[len(e.args)-1].atom, params)
		if err != nil {
			return "", nil, err
		}
		segs := e.args[:len(e.args)-1] // 路径段（除值）
		return s.lispThrough(segs, val, *td, g)
	case "in":
		// (in 字段 (subtree "slug")) — 集合内嵌
		if len(e.args) != 2 {
			return "", nil, fmt.Errorf("filter-lisp: in takes 2 args")
		}
		field, _ := e.args[0].atom.(string)
		sub := e.args[1]
		if sub.head == "subtree" {
			slug, _ := sub.args[0].atom.(string)
			cat, err := s.GetBySlug(slug)
			if err != nil || cat == nil {
				return "", nil, fmt.Errorf("filter-lisp: subtree %q not found", slug)
			}
			ids, err := s.Subtree(cat.ID, "parent", 20)
			if err != nil {
				return "", nil, err
			}
			ids = append([]int64{cat.ID}, ids...)
			anyIDs := make([]any, len(ids))
			for i, id := range ids {
				anyIDs[i] = id
			}
			return "EXISTS(SELECT 1 FROM edges WHERE field = " + g.bind() + " AND from_node = nodes.id AND to_node IN (" + g.expand() + "))",
				[]any{field, anyIDs}, nil
		}
		return "", nil, fmt.Errorf("filter-lisp: in only supports (subtree ...) now")
	}
	return "", nil, fmt.Errorf("filter-lisp: unknown %q", e.head)
}

// lispThrough 递归编译穿透路径: [引用段... 目标段] → 嵌套 EXISTS。
// 每段: EXISTS(edges e JOIN nodes t ... e.field = 段 AND link AND (下一段|目标条件))。
func (s *Service) lispThrough(segs []lispExpr, val any, td types.TypeDef, g phGen) (string, []any, error) {
	if len(segs) == 0 {
		return "", nil, fmt.Errorf("filter-lisp: get empty path")
	}
	// 最后一段: 目标字段（$.x 或列）
	last := segs[len(segs)-1]
	targetField, _ := last.atom.(string)
	if len(segs) == 1 {
		// 只有目标字段（无引用）: 不可能 — get 至少一段引用
		return "", nil, fmt.Errorf("filter-lisp: get needs ref segments")
	}
	// 从后往前递归: 段[i] 是引用, 其目标类型 = 段[i+1] 的宿主类型
	// link 是当前层的关联节点（第一层 nodes.id, 内层 t{i}.id — 上一层 JOIN 目标）;
	// 别名带层级（e{i}/t{i}）— 嵌套 EXISTS 共用别名会内层遮蔽外层。
	var build func(i int, link string) (string, []any, error)
	build = func(i int, link string) (string, []any, error) {
		segName, _ := segs[i].atom.(string)
		// 查段 i 的引用字段（宿主类型 = 段 i-1 的目标; 段0 宿主 = td）
		host := td
		if i > 0 {
			// 段 i-1 是引用 — 宿主类型是它的目标
			prevField, _ := segs[i-1].atom.(string)
			to, err := s.refTargetType(host, prevField)
			if err != nil {
				return "", nil, err
			}
			htd, _ := s.types.Type(to)
			host = htd
		}
		// 段 i 必须是引用（除最后段是目标字段）
		to, err := s.refTargetType(host, segName)
		if err != nil {
			return "", nil, err
		}
		_ = to
		// 下一段的条件（递归）或目标字段
		var inner string
		var innerArgs []any
		if i == len(segs)-2 {
			// 下一段是目标字段（用最后层别名 t{len(segs)} — 目标节点）
			ta := fmt.Sprintf("t%d", i+1)
			if strings.HasPrefix(targetField, "$.") {
				f2 := strings.TrimPrefix(targetField, "$.")
				inner = "json_extract(" + ta + ".fields, " + g.bind() + ") = " + g.bind()
				innerArgs = []any{"$." + f2, val}
			} else {
				inner = ta + "." + g.quote() + " = " + g.bind()
				innerArgs = []any{targetField, val}
			}
		} else {
			// 内层: 关联上一层 JOIN 目标（t{i}.id）
			inner, innerArgs, err = build(i+1, fmt.Sprintf("t%d.id", i+1))
			if err != nil {
				return "", nil, err
			}
		}
		frag := fmt.Sprintf("EXISTS(SELECT 1 FROM edges e%d JOIN nodes t%d ON t%d.id = e%d.to_node WHERE e%d.field = ", i+1, i+1, i+1, i+1, i+1) +
			g.bind() + " AND e" + fmt.Sprintf("%d", i+1) + ".from_node = " + link + " AND " + inner + ")"
		return frag, append(innerArgs, segName), nil
	}
	return build(0, "nodes.id")
}

// refTargetType 宿主类型里引用字段的目标类型。
func (s *Service) refTargetType(td types.TypeDef, field string) (string, error) {
	for _, f := range td.Fields {
		if f.Name == field && s.types.IsRefKind(f.Kind) {
			return f.To, nil
		}
	}
	return "", fmt.Errorf("filter-lisp: %q not a ref field on %q", field, td.Name)
}
