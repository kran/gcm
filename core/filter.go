package core

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kran/gcm/types"
)

// ── Filter: 查询表达式引擎（PocketBase 风格 + 占位符）────────
//
// 语法:
//
//	filter = "status = 1 && categories ~ {:cat} && $.title like {:kw}"
//
//	比较:   [<-] 路径 操作符 值 | <- 路径（入边存在性）
//	路径:   段 ("." 段)*; 段 = "$.字段"（fields JSON）| "字段"（列/引用）
//	操作符: = != > >= < <= like ~（~ = 引用包含 或 字符串 LIKE, 由类型决定）
//	值:     "字符串" | 数字 | true/false | {占位符}（运行时绑定, 参数化）
//
// 编译: 类型定义驱动 — 字段存在/操作符匹配/穿透类型链, 编译期校验（fail-loud）。
// 占位符: BuildFilter(params) 时填入, 生成参数化 WHERE（防注入）。

// ── Token ──────────────────────────────────────────

type tokKind int

const (
	tkIdent tokKind = iota
	tkOp            // = != > >= < <= ~ like <-（入边前缀）
	tkDot           // 穿透分隔
	tkAnd
	tkOr
	tkNot
	tkLParen
	tkRParen
	tkString
	tkNumber
	tkBool
	tkPlaceholder
	tkLBracket
	tkRBracket
	tkComma
	tkEOF
)

type token struct {
	kind tokKind
	text string
	line int
}

func lex(src string) ([]token, error) {
	var out []token
	i, line := 0, 1
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if c == '\n' {
				line++
			}
			i++
		case c == '(':
			out = append(out, token{tkLParen, "(", line})
			i++
		case c == ')':
			out = append(out, token{tkRParen, ")", line})
			i++
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				out = append(out, token{tkOp, "!=", line})
				i += 2
			} else {
				out = append(out, token{tkNot, "!", line})
				i++
			}
		case c == '&' && i+1 < len(src) && src[i+1] == '&':
			out = append(out, token{tkAnd, "&&", line})
			i += 2
		case c == '|' && i+1 < len(src) && src[i+1] == '|':
			out = append(out, token{tkOr, "||", line})
			i += 2
		case c == '=':
			out = append(out, token{tkOp, "=", line})
			i++
		case c == '>' || c == '<':
			op := string(c)
			i++
			if i < len(src) && src[i] == '=' {
				op += "="
				i++
			} else if c == '<' && i < len(src) && src[i] == '-' {
				// <- 入边前缀
				op = "<-"
				i++
			}
			out = append(out, token{tkOp, op, line})
		case c == '~':
			out = append(out, token{tkOp, "~", line})
			i++
		case c == '[':
			out = append(out, token{tkLBracket, "[", line})
			i++
		case c == ']':
			out = append(out, token{tkRBracket, "]", line})
			i++
		case c == ',':
			out = append(out, token{tkComma, ",", line})
			i++
		case c == '.':
			out = append(out, token{tkDot, ".", line})
			i++
		case c == '$' && i+1 < len(src) && src[i+1] == '.':
			j := i + 2
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			if j == i+2 {
				return nil, fmt.Errorf("filter: line %d: $. requires field name", line)
			}
			out = append(out, token{tkIdent, "$." + src[i+2:j], line})
			i = j
		case c == '{':
			// 占位符 {:name}（PB 格式）— { 后必须紧跟 :
			if i+1 >= len(src) || src[i+1] != ':' {
				return nil, fmt.Errorf("filter: line %d: unexpected '{' (placeholder format is {:name})", line)
			}
			j := i + 2
			for j < len(src) && src[j] != '}' {
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("filter: line %d: unterminated placeholder", line)
			}
			name := src[i+2 : j]
			if name == "" || !isIdent(name) {
				return nil, fmt.Errorf("filter: line %d: invalid placeholder {%s}", line, name)
			}
			out = append(out, token{tkPlaceholder, name, line})
			i = j + 1
		case c == '"' || c == '\'':
			q := c
			j := i + 1
			var sb strings.Builder
			for j < len(src) && src[j] != q {
				if src[j] == '\\' && j+1 < len(src) {
					j++
				}
				sb.WriteByte(src[j])
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("filter: line %d: unterminated string", line)
			}
			out = append(out, token{tkString, sb.String(), line})
			i = j + 1
		case c >= '0' && c <= '9' || c == '-' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			j := i + 1
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			out = append(out, token{tkNumber, src[i:j], line})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			word := src[i:j]
			switch word {
			case "like":
				out = append(out, token{tkOp, "like", line})
			case "true", "false":
				out = append(out, token{tkBool, word, line})
			default:
				out = append(out, token{tkIdent, word, line})
			}
			i = j
		default:
			return nil, fmt.Errorf("filter: line %d: unexpected char %q", line, c)
		}
	}
	out = append(out, token{tkEOF, "", line})
	return out, nil
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '-'
}

func isIdent(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			return false
		}
	}
	return true
}

// ── AST ────────────────────────────────────────────

type Expr interface{ isExpr() }

type andExpr struct{ L, R Expr }
type orExpr struct{ L, R Expr }
type notExpr struct{ X Expr }
type cmpExpr struct {
	in    bool        // "<-" 入边
	path  []types.Seg // 统一路径语言（types.ParsePath）
	op    string
	value any // string / float64 / bool / placeholder
}
type placeholder struct{ name string }

func (andExpr) isExpr() {}
func (orExpr) isExpr()  {}
func (notExpr) isExpr() {}
func (cmpExpr) isExpr() {}

// ── Parser（递归下降）──────────────────────────────

type parser struct {
	toks []token
	pos  int
}

func parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tkEOF {
		return nil, fmt.Errorf("filter: line %d: unexpected %q", p.cur().line, p.cur().text)
	}
	return e, nil
}

func (p *parser) cur() token  { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tkOr {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = orExpr{l, r}
	}
	return l, nil
}

func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tkAnd {
		p.next()
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = andExpr{l, r}
	}
	return l, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.cur().kind == tkNot {
		p.next()
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notExpr{x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	if p.cur().kind == tkLParen {
		p.next()
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tkRParen {
			return nil, fmt.Errorf("filter: line %d: expected )", p.cur().line)
		}
		p.next()
		return e, nil
	}
	return p.parseCmp()
}

// parseCmp: [<-] path op value | <- path（入边存在性）
func (p *parser) parseCmp() (Expr, error) {
	c := &cmpExpr{}
	if p.cur().kind == tkOp && p.cur().text == "<-" {
		c.in = true
		p.next()
	}
	// 路径: 段 ("." 段)* → 统一路径语言（与 expand/title 同一抽象）
	var segs []string
	for {
		t := p.cur()
		if t.kind != tkIdent {
			return nil, fmt.Errorf("filter: line %d: expected field, got %q", t.line, t.text)
		}
		segs = append(segs, t.text)
		p.next()
		if p.cur().kind == tkDot {
			p.next()
			continue
		}
		break
	}
	raw := strings.Join(segs, ".")
	if c.in {
		raw = "<-" + raw
	}
	path, err := types.ParsePath(raw)
	if err != nil {
		return nil, fmt.Errorf("filter: line %d: %w", p.cur().line, err)
	}
	c.path = path
	// 入边存在性: 无操作符
	if c.in && (p.cur().kind == tkEOF || p.cur().kind == tkAnd || p.cur().kind == tkOr || p.cur().kind == tkRParen) {
		return c, nil
	}
	if p.cur().kind != tkOp {
		return nil, fmt.Errorf("filter: line %d: expected operator, got %q", p.cur().line, p.cur().text)
	}
	c.op = p.next().text
	v, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	c.value = v
	return c, nil
}

func (p *parser) parseValue() (any, error) {
	t := p.cur()
	switch t.kind {
	case tkString:
		p.next()
		return t.text, nil
	case tkNumber:
		p.next()
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("filter: line %d: invalid number %q", t.line, t.text)
		}
		return f, nil
	case tkBool:
		p.next()
		return t.text == "true", nil
	case tkPlaceholder:
		p.next()
		return placeholder{name: t.text}, nil
	case tkLBracket:
		// 数组字面量 [1, 2, 3] — 元素: 数字 / {:占位符}（id 集合, ~ 专用）
		p.next()
		var items []any
		if p.cur().kind == tkRBracket {
			p.next()
			return items, nil
		}
		for {
			t := p.cur()
			switch t.kind {
			case tkNumber:
				p.next()
				f, err := strconv.ParseFloat(t.text, 64)
				if err != nil {
					return nil, fmt.Errorf("filter: line %d: invalid number %q", t.line, t.text)
				}
				items = append(items, f)
			case tkPlaceholder:
				p.next()
				items = append(items, placeholder{name: t.text})
			default:
				return nil, fmt.Errorf("filter: line %d: array elements must be numbers or {:placeholders}, got %q", t.line, t.text)
			}
			if p.cur().kind == tkComma {
				p.next()
				continue
			}
			break
		}
		if p.cur().kind != tkRBracket {
			return nil, fmt.Errorf("filter: line %d: expected ']', got %q", p.cur().line, p.cur().text)
		}
		p.next()
		return items, nil
	default:
		return nil, fmt.Errorf("filter: line %d: expected value, got %q", t.line, t.text)
	}
}

// CompiledFilter 编译后的 filter: 校验完成, BuildFilter 填占位符生成 WHERE。
type CompiledFilter struct {
	src        string
	expr       Expr
	placeholds []string
}

// CompileFilter 编译并校验 filter（语法 + 占位符收集; 字段校验在 BuildFilter）。
// 编译结果按表达式缓存（Service 级）: 渲染期/admin 高频同表达式共享,
// 编译一次 — 纯函数职责也借此留在引擎（消费方无需自建缓存）。
func (s *Service) CompileFilter(expr string) (*CompiledFilter, error) {
	if v, ok := s.filterCache.Load(expr); ok {
		return v.(*CompiledFilter), nil
	}
	e, err := parse(expr)
	if err != nil {
		return nil, err
	}
	cf := &CompiledFilter{src: expr, expr: e}
	s.collectPlaceholders(e, &cf.placeholds)
	s.filterCache.Store(expr, cf)
	return cf, nil
}

func (s *Service) collectPlaceholders(e Expr, out *[]string) {
	switch x := e.(type) {
	case andExpr:
		s.collectPlaceholders(x.L, out)
		s.collectPlaceholders(x.R, out)
	case orExpr:
		s.collectPlaceholders(x.L, out)
		s.collectPlaceholders(x.R, out)
	case notExpr:
		s.collectPlaceholders(x.X, out)
	case *cmpExpr:
		if p, ok := x.value.(placeholder); ok {
			*out = append(*out, p.name)
		}
	}
}

// BuildFilter 填占位符并生成 WHERE 条件片段（dba 占位符从 #{1} 起）。
// 片段作为**独立 Add 段**使用（dba 每段独立计数）— 调用方链式:
//
//	db.Add(`SELECT * FROM nodes WHERE type = #{1}`, typ).
//	   Add(`AND `+where, args...)
func (s *Service) BuildFilter(cf *CompiledFilter, typeName string, params map[string]any) (string, []any, error) {
	td, ok := s.types.Type(typeName)
	if !ok {
		return "", nil, fmt.Errorf("core: type %q not defined", typeName)
	}
	for _, name := range cf.placeholds {
		if _, ok := params[name]; !ok {
			return "", nil, fmt.Errorf("filter: placeholder {: %s} not bound", name)
		}
	}
	var args []any
	var sb strings.Builder
	idx := 1
	if err := s.buildExpr(cf.expr, &td, params, &sb, &args, &idx); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

func (s *Service) buildExpr(e Expr, td *types.TypeDef, params map[string]any, sb *strings.Builder, args *[]any, idx *int) error {
	switch x := e.(type) {
	case andExpr:
		sb.WriteString("(")
		if err := s.buildExpr(x.L, td, params, sb, args, idx); err != nil {
			return err
		}
		sb.WriteString(" AND ")
		if err := s.buildExpr(x.R, td, params, sb, args, idx); err != nil {
			return err
		}
		sb.WriteString(")")
		return nil
	case orExpr:
		sb.WriteString("(")
		if err := s.buildExpr(x.L, td, params, sb, args, idx); err != nil {
			return err
		}
		sb.WriteString(" OR ")
		if err := s.buildExpr(x.R, td, params, sb, args, idx); err != nil {
			return err
		}
		sb.WriteString(")")
		return nil
	case notExpr:
		sb.WriteString("NOT (")
		if err := s.buildExpr(x.X, td, params, sb, args, idx); err != nil {
			return err
		}
		sb.WriteString(")")
		return nil
	case *cmpExpr:
		frag, fargs, err := s.buildCmp(x, td, params, idx)
		if err != nil {
			return err
		}
		sb.WriteString(frag)
		*args = append(*args, fargs...)
		return nil
	default:
		return fmt.Errorf("filter: unknown expr (%T)", e)
	}
}

func (s *Service) buildCmp(c *cmpExpr, td *types.TypeDef, params map[string]any, idx *int) (string, []any, error) {
	seg := c.path[0]
	val, valErr := valueParam(c.value, params)
	if valErr != nil {
		return "", nil, valErr
	}
	g := phGen{idx}
	ph, qi := g.bind, g.quote
	// $.字段: 本节点 JSON 标量
	if seg.JSON {
		f, ok := types.FieldByName(*td, seg.Field)
		if !ok {
			return "", nil, fmt.Errorf("filter: field %q not on type %q", seg.Field, td.Name)
		}
		if s.types.IsRefKind(f.Kind) {
			return "", nil, fmt.Errorf("filter: field %q is ref, use without $. (reference filter)", seg.Field)
		}
		if c.op == "~" {
			return "", nil, fmt.Errorf("filter: JSON field %q uses like, not ~ (reference operator)", seg.Field)
		}
		return "json_extract(nodes.fields, " + ph() + ") " + sqlOp(c.op) + " " + ph(), []any{"$." + seg.Field, val}, nil
	}
	// 无前缀: 列（列名经 dba quote 管道 — 标识符不拼字符串）
	if types.IsNodeColumn(seg.Field) {
		if c.op == "~" {
			return "", nil, fmt.Errorf("filter: column %q uses like, not ~ (reference operator)", seg.Field)
		}
		return "nodes." + qi() + " " + sqlOp(c.op) + " " + ph(), []any{seg.Field, val}, nil
	}
	// 引用: 字段查宿主类型（出边）或全局（入边 — 字段属于引用方类型）
	var f types.FieldDef
	if c.in {
		var err error
		f, _, err = s.refFieldMetaGlobal(seg.Field)
		if err != nil {
			return "", nil, err
		}
	} else {
		var ok bool
		f, ok = types.FieldByName(*td, seg.Field)
		if !ok {
			return "", nil, fmt.Errorf("filter: field %q not on type %q", seg.Field, td.Name)
		}
		if !s.types.IsRefKind(f.Kind) {
			return "", nil, fmt.Errorf("filter: field %q is a JSON field, use $.%s", seg.Field, seg.Field)
		}
	}
	// 操作符按形态校验（仅直接引用; 穿透时操作符属于目标字段）:
	// 入边存在性（op==""）允许; ref[] 只 ~; ref 单值 ~ 或 =
	kind, _ := s.types.Kind(f.Kind)
	if len(c.path) > 1 {
		return s.refThroughCmp(c, f, val, params, idx)
	}
	if c.op == "" {
		if !c.in {
			return "", nil, fmt.Errorf("filter: missing operator after %q", seg.Field)
		}
	} else if kind.Class() == types.ClassRefList && c.op != "~" {
		return "", nil, fmt.Errorf("filter: ref[] field %q supports ~, got %q", seg.Field, c.op)
	}
	if kind.Class() == types.ClassRef && c.op != "~" && c.op != "=" {
		return "", nil, fmt.Errorf("filter: ref field %q supports ~ or =, got %q", seg.Field, c.op)
	}
	// 直接引用
	return s.refCmp(c, f, val, params, idx)
}

func (s *Service) refCmp(c *cmpExpr, f types.FieldDef, val any, params map[string]any, idx *int) (string, []any, error) {
	if c.op != "" && c.op != "~" && c.op != "=" {
		return "", nil, fmt.Errorf("filter: ref field %q supports ~ or =, got %q", f.Name, c.op)
	}
	g := phGen{idx}
	ph, exp := g.bind, g.expand
	// 数组值: id 集合（~ 专用）→ IN (#{n|expand})
	if arr, ok := val.([]any); ok {
		if c.op != "~" {
			return "", nil, fmt.Errorf("filter: array value only valid with ~, got %q", c.op)
		}
		if len(arr) == 0 {
			return "", nil, fmt.Errorf("filter: empty array for ref field %q", f.Name)
		}
		if c.in {
			return "EXISTS(SELECT 1 FROM edges WHERE field = " + ph() + " AND to_node = nodes.id AND from_node IN (" + exp() + "))", []any{f.Name, arr}, nil
		}
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + ph() + " AND from_node = nodes.id AND to_node IN (" + exp() + "))", []any{f.Name, arr}, nil
	}
	// 方向与关联: 出边 = 我引用谁（from_node = 本节点, to_node = 值）;
	// 入边 = 谁引用我（to_node = 本节点, from_node = 值）。参数化模板,
	// 否定 = NOT 前缀 — 方向×否定 4 态收敛为 2 个模板。
	link, other := "from_node = nodes.id AND to_node", "to_node = nodes.id AND from_node"
	if c.in {
		link, other = other, link
	}
	neg := ""
	if c.op == "!=" {
		neg = "NOT "
	}
	// 入边存在性（无值）
	if c.op == "" && c.in {
		return "EXISTS(SELECT 1 FROM edges WHERE field = " + ph() + " AND to_node = nodes.id)", []any{f.Name}, nil
	}
	return neg + "EXISTS(SELECT 1 FROM edges WHERE field = " + ph() +
		" AND " + link + " = " + ph() + ")", []any{f.Name, val}, nil
}

func (s *Service) refThroughCmp(c *cmpExpr, f types.FieldDef, val any, params map[string]any, idx *int) (string, []any, error) {
	if len(c.path) > 2 {
		return "", nil, fmt.Errorf("filter: through path depth > 2 not supported")
	}
	targetType, ok := s.types.Type(f.To)
	if !ok {
		return "", nil, fmt.Errorf("filter: ref %q target type %q not defined", f.Name, f.To)
	}
	seg2 := c.path[1]
	col := "to_node"
	if c.in {
		col = "from_node"
	}
	g := phGen{idx}
	ph, qi := g.bind, g.quote
	var cond string
	var condArgs []any
	if seg2.JSON {
		tf, ok := types.FieldByName(targetType, seg2.Field)
		if !ok {
			return "", nil, fmt.Errorf("filter: field %q not on type %q", seg2.Field, targetType.Name)
		}
		if s.types.IsRefKind(tf.Kind) {
			return "", nil, fmt.Errorf("filter: nested ref not supported in through path")
		}
		if c.op == "~" {
			return "", nil, fmt.Errorf("filter: through JSON field %q uses like, not ~", seg2.Field)
		}
		cond = "json_extract(t.fields, " + ph() + ") " + sqlOp(c.op) + " " + ph()
		condArgs = []any{"$." + seg2.Field, val}
	} else {
		if !types.IsNodeColumn(seg2.Field) {
			tf, ok := types.FieldByName(targetType, seg2.Field)
			if !ok {
				return "", nil, fmt.Errorf("filter: field %q not on type %q", seg2.Field, targetType.Name)
			}
			if !s.types.IsRefKind(tf.Kind) {
				return "", nil, fmt.Errorf("filter: field %q is a JSON field, use $.%s", seg2.Field, seg2.Field)
			}
			return "", nil, fmt.Errorf("filter: nested ref not supported in through path")
		}
		cond = "t." + qi() + " " + sqlOp(c.op) + " " + ph()
		condArgs = []any{seg2.Field, val}
	}
	link := "e.from_node = nodes.id"
	if c.in {
		link = "e.to_node = nodes.id"
	}
	// 参数按占位符编号顺序: cond（先编号）→ col → field
	args := append(condArgs, col, f.Name)
	return "EXISTS(SELECT 1 FROM edges e JOIN nodes t ON t.id = e." + qi() +
		" WHERE e.field = " + ph() + " AND " + link + " AND " + cond + ")", args, nil
}

// phGen dba 占位符生成器（buildCmp/refCmp/refThroughCmp 共享）:
// 三种占位符形态 — 值绑定 / 标识符引用 / 切片展开。
type phGen struct{ idx *int }

func (g phGen) bind() string   { n := *g.idx; *g.idx++; return fmt.Sprintf("#{%d}", n) }
func (g phGen) quote() string  { n := *g.idx; *g.idx++; return fmt.Sprintf("#{%d|quote}", n) }
func (g phGen) expand() string { n := *g.idx; *g.idx++; return fmt.Sprintf("#{%d|expand}", n) }

// valueParam 值/占位符/数组 → 参数值（数组元素逐个解析占位符）。
func valueParam(v any, params map[string]any) (any, error) {
	switch t := v.(type) {
	case placeholder:
		val, ok := params[t.name]
		if !ok {
			return nil, fmt.Errorf("filter: placeholder {: %s} not bound", t.name)
		}
		return val, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			if p, ok := item.(placeholder); ok {
				val, ok := params[p.name]
				if !ok {
					return nil, fmt.Errorf("filter: placeholder {: %s} not bound", p.name)
				}
				out[i] = val
			} else {
				out[i] = item
			}
		}
		return out, nil
	}
	return v, nil
}

func sqlOp(op string) string {
	if op == "like" {
		return "LIKE"
	}
	return op
}
