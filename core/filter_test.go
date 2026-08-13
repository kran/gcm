package core

import (
	"fmt"
	"strings"
	"testing"
)

const filterTypes = `
types:
  category:
    fields:
      - { name: name, kind: string }
      - { name: parent, kind: ref, to: category }
  person:
    title: name
    search: true
    fields:
      - { name: name, kind: string, required: true }
      - { name: level, kind: string }
  article:
    title: title
    search: true
    fields:
      - { name: title, kind: string, required: true }
      - { name: body, kind: richtext }
      - { name: featured, kind: bool }
      - { name: views, kind: number }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
`

// 标准: 列比较 + 逻辑。
func TestFilterBasic(t *testing.T) {
	s := newFilterSvc(t)
	s.Create(&Node{Type: "article", Title: "甲", Status: StatusPublished, Sort: 1,
		Fields: Fields{"title": "甲", "body": "x"}})
	s.Create(&Node{Type: "article", Title: "乙", Status: StatusDraft, Sort: 2,
		Fields: Fields{"title": "乙", "body": "y"}})

	cf, err := s.CompileFilter(`status = 1 && sort >= 1`)
	if err != nil {
		t.Fatal(err)
	}
	where, args, err := s.BuildFilter(cf, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("article", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "甲" {
		t.Fatalf("basic: %d %v", len(list), list)
	}
}

// JSON 字段: $.featured / $.views。
func TestFilterJSON(t *testing.T) {
	s := newFilterSvc(t)
	s.Create(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "a", "featured": true, "views": 100}})
	s.Create(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "b", "featured": false, "views": 5}})

	cf, _ := s.CompileFilter(`$.featured = true && $.views > 10`)
	where, args, err := s.BuildFilter(cf, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("article", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "a" {
		t.Fatalf("json: %d", len(list))
	}
}

// 引用: authors ~ 5（出边）。
func TestFilterRef(t *testing.T) {
	s := newFilterSvc(t)
	p1, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	p2, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "李四"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "a", "authors": []any{p1}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "b", "authors": []any{p2}}})

	cf, _ := s.CompileFilter(`authors ~ {:author}`)
	where, args, err := s.BuildFilter(cf, "article", map[string]any{"author": p1})
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("article", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "a" {
		t.Fatalf("ref: %d", len(list))
	}
	// 占位符未绑定 fail
	if _, _, err := s.BuildFilter(cf, "article", map[string]any{}); err == nil {
		t.Fatal("unbound placeholder must fail")
	}
}

// 入边: <-authors（存在性）和 <-authors ~ id。
func TestFilterRefIn(t *testing.T) {
	s := newFilterSvc(t)
	p1, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	p2, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "李四"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "a", "authors": []any{p1}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "b", "authors": []any{p2}}})

	// 存在性: 被文章引用为作者的人（张三/李四）
	cf, _ := s.CompileFilter(`<-authors`)
	where, args, err := s.BuildFilter(cf, "person", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("person", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("in exists: %d", len(list))
	}
	// 具体: 是文章 X 的作者的人
	artID := list[0].ID
	_ = artID
	// 查"是文章 a 作者的人" — 需要文章 id, 简化: 通过出边建另一人
	p3, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "王五"}})
	_ = p3
	cf2, _ := s.CompileFilter(`<-authors ~ {:art}`)
	where2, args2, _ := s.BuildFilter(cf2, "person", map[string]any{"art": p1})
	// p1 是文章 a 的作者, 但 p1 不是文章 — 入边到 p1 的是"文章引用 p1" — 查 person 集合:
	// "文章 X 引用了谁" = person filter <-authors ~ X — 语义反了; 正确: 出边 authors ~ X 从文章查
	_ = where2
	_ = args2
	// 改为: 从 person 查 "被文章 a 引用"（a 是文章 id）
	arts, _, _ := s.List("article", -1, 1, 10)
	aid := int64(0)
	for _, a := range arts {
		if a.Fields["title"] == "a" {
			aid = a.ID
		}
	}
	cf3, _ := s.CompileFilter(`<-authors ~ {:art}`)
	where3, args3, _ := s.BuildFilter(cf3, "person", map[string]any{"art": aid})
	list3, _, _ := s.ListFiltered("person", where3, args3, 1, 10)
	if len(list3) != 1 || list3[0].Fields["name"] != "张三" {
		t.Fatalf("in specific: %d %v", len(list3), list3)
	}
}

// 穿透: authors.$.level（作者级别）。
func TestFilterThrough(t *testing.T) {
	s := newFilterSvc(t)
	p1, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "张三", "level": "senior"}})
	p2, _ := s.Create(&Node{Type: "person", Fields: Fields{"name": "李四", "level": "junior"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "a", "authors": []any{p1}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "b", "authors": []any{p2}}})

	cf, _ := s.CompileFilter(`authors.$.level = "senior"`)
	where, args, err := s.BuildFilter(cf, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("article", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "a" {
		t.Fatalf("through: %d", len(list))
	}
}

// 组合: 括号 + OR + NOT。
func TestFilterComposite(t *testing.T) {
	s := newFilterSvc(t)
	cat1, _ := s.Create(&Node{Type: "category", Fields: Fields{"name": "行业"}})
	cat2, _ := s.Create(&Node{Type: "category", Fields: Fields{"name": "区域"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "a", "categories": []any{cat1}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "b", "categories": []any{cat2}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "c"}})

	cf, _ := s.CompileFilter(`(categories ~ {:c1} || categories ~ {:c2}) && status = 1`)
	where, args, err := s.BuildFilter(cf, "article",
		map[string]any{"c1": cat1, "c2": cat2})
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("article", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("composite: %d", len(list))
	}
	// NOT
	cf2, _ := s.CompileFilter(`!(categories ~ {:c1})`)
	where2, args2, _ := s.BuildFilter(cf2, "article", map[string]any{"c1": cat1})
	list2, _, _ := s.ListFiltered("article", where2, args2, 1, 10)
	if len(list2) != 2 {
		t.Fatalf("not: %d", len(list2))
	}
}

// 校验 fail-loud: 未知字段 / JSON 用于引用 / 引用用于 JSON / 穿透嵌套。
func TestFilterValidation(t *testing.T) {
	s := newFilterSvc(t)
	cases := []string{
		`ghost = 1`,
		`$.authors ~ 5`,         // 引用用 $. 前缀
		`authors = "x"`,         // ref 用 = 字符串
		`$.title ~ 5`,           // JSON 标量用 ~（string 字段支持 ~? 我们 ~ 只给引用 — 校验）
		`authors.$.level ~ "x"`, // 穿透嵌套
		`authors.ghost = 1`,     // 穿透未知字段
	}
	for _, c := range cases {
		cf, err := s.CompileFilter(c)
		if err == nil {
			// 编译只查语法; 字段校验在 Build
			if _, _, err := s.BuildFilter(cf, "article", nil); err == nil {
				t.Fatalf("must fail: %s", c)
			}
		}
	}
}

func newFilterSvc(t *testing.T) *Service {
	t.Helper()
	return New(testDB(t), newTypes(t, filterTypes))
}

var _ = strings.Contains

// ── 边界情况: parser/编译的鲁棒性 ─────────────────

// 边界: 合法输入应编译通过（结果合理）。
func TestFilterEdgeValid(t *testing.T) {
	s := newFilterSvc(t)
	valid := []string{
		`status = 1`,
		`((status = 1))`,
		`status = 1 && (sort > 2 || sort < 0)`,
		`title like "a"`,
		`status = {:x}`,
	}
	for _, expr := range valid {
		cf, err := s.CompileFilter(expr)
		if err != nil {
			t.Fatalf("valid %q must compile: %v", expr, err)
		}
		if _, _, err := s.BuildFilter(cf, "article", map[string]any{"x": 1}); err != nil {
			t.Fatalf("valid %q must build: %v", expr, err)
		}
	}
}

// 边界: 非法输入必须 fail-loud（不 panic 不静默）。
func TestFilterEdgeInvalid(t *testing.T) {
	s := newFilterSvc(t)
	invalid := []string{
		``,
		`   `,
		`()`,
		`status =`,      // 缺值
		`= 1`,           // 缺字段
		`status == 1`,   // 双等号
		`1 = status`,    // 值在左 — 语法会报? 实际 lexer: number 开头 → 字段期望 → 报错
		`authors ~`,     // 缺值
		`$.`,            // $. 无字段
		`{:}`,           // 空占位符
		`{ :x}`,         // 占位符格式错
		`status = 1 &&`, // 尾部 &&
		`~ 5`,           // 无字段
		`@status = 1`,   // 非法字符
		`status = 1)`,   // 多余括号
		`(status = 1`,   // 缺括号
		`title ~ "x"`,   // 列用 ~（编译期拒绝）
		`$.body ~ 5`,    // JSON 用 ~
		`authors = "x"`, // ref[] 用 =
		`a.b.c.d = 1`,   // 穿透超深
	}
	for _, expr := range invalid {
		cf, err := s.CompileFilter(expr)
		if err != nil {
			continue // 语法层拒绝 ✓
		}
		if _, _, err := s.BuildFilter(cf, "article", nil); err == nil {
			t.Fatalf("invalid %q must fail", expr)
		}
	}
}

// 字符串转义: 引号内内容不破坏表达式。
func TestFilterEdgeEscape(t *testing.T) {
	s := newFilterSvc(t)
	cf, err := s.CompileFilter(`$.title like "a\"b" && status = 1`)
	if err != nil {
		t.Fatalf("escape must compile: %v", err)
	}
	where, args, err := s.BuildFilter(cf, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 参数化: [JSON路径, 值, 列名, 值] — 转义值原样保留
	if len(args) != 4 || args[1] != `a"b` || args[0] != "$.title" {
		t.Fatalf("escaped value: %v", args)
	}
	_ = where
}

// 数组字面量: categories ~ [ids] — 子树过滤（树节点 DFS 集合的落点）。
func TestFilterArrayRef(t *testing.T) {
	s := newFilterSvc(t)
	// 分类树: 根1 → 子2; 根3
	c1, _ := s.Create(&Node{Type: "category", Fields: Fields{"name": "根1"}})
	c2, _ := s.Create(&Node{Type: "category", Fields: Fields{"name": "子2", "parent": c1}})
	c3, _ := s.Create(&Node{Type: "category", Fields: Fields{"name": "根3"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "a", "categories": []any{c2}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "b", "categories": []any{c3}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "c"}})

	// 子树集合 [c1, c2] → a（c2 的文章）命中; b/c 不命中
	cf, err := s.CompileFilter(fmt.Sprintf(`categories ~ [%d, %d]`, c1, c2))
	if err != nil {
		t.Fatal(err)
	}
	where, args, err := s.BuildFilter(cf, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListFiltered("article", where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "a" {
		t.Fatalf("array ref: %d %v", len(list), list)
	}

	// 占位符数组: categories ~ {:ids}
	cf2, _ := s.CompileFilter(`categories ~ {:ids}`)
	where2, args2, err := s.BuildFilter(cf2, "article", map[string]any{"ids": []any{c2, c1}})
	if err != nil {
		t.Fatal(err)
	}
	list2, _, _ := s.ListFiltered("article", where2, args2, 1, 10)
	if len(list2) != 1 {
		t.Fatalf("placeholder array: %d", len(list2))
	}

	// 校验: 数组 + 非 ~ 拒绝; 空数组拒绝
	cf3, _ := s.CompileFilter(`categories = [1, 2]`)
	if _, _, err := s.BuildFilter(cf3, "article", nil); err == nil {
		t.Fatal("array with = must fail")
	}
	cf4, _ := s.CompileFilter(`categories ~ []`)
	if _, _, err := s.BuildFilter(cf4, "article", nil); err == nil {
		t.Fatal("empty array must fail")
	}
}
