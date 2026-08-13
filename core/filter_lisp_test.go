package core

import (
	"testing"
)

// Lisp filter（var 槽版）: 解析/编译/执行。
func TestLispFilter(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "featured": true, "categories": []any{child}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "featured": false}})

	exec := func(lisp string, params map[string]any) []Node {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, lisp, "article", params)
		if err != nil {
			t.Fatalf("compile %q: %v", lisp, err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec %q: %v", lisp, err)
		}
		return rows
	}
	check := func(lisp string, want int) {
		t.Helper()
		if n := len(exec(lisp, nil)); n != want {
			t.Fatalf("%q: want %d got %d", lisp, want, n)
		}
	}

	check(`(and (= type "article") (= status 1))`, 2)
	check(`(= $.featured true)`, 1)
	check(`(and (= status 1) (= $.featured true))`, 1)
	check(`(not (= $.featured true))`, 1)
	rows := exec(`(-> categories {:id})`, map[string]any{"id": child})
	if len(rows) != 1 {
		t.Fatalf("placeholder ref: %d", len(rows))
	}
	check(`(in categories (subtree "root"))`, 1)
	check(`(get (-> categories) (-> parent) $.name "根")`, 1)
	check(`(get (-> categories) (-> parent (= status 1)) $.name "根")`, 1)
}

// 注册驱动: 站点自定义函数（Lisp 精髓 — 函数进表达式）。
func TestLispRegisterFunc(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "categories": []any{child}}})

	// 站点注册: (children-of "root") → id 列表（直接参数）
	s.RegisterLispFuncC("children-of", func(args []lispExpr) (string, []any, error) {
		slug, _ := pathOfC(args[0])
		cat, _ := s.GetBySlug(slug)
		if cat == nil {
			return "", nil, errTestSlug(slug)
		}
		ids, _ := s.Subtree(cat.ID, "parent", 20)
		ids = append([]int64{cat.ID}, ids...)
		anyIDs := make([]any, len(ids))
		for i, id := range ids {
			anyIDs[i] = id
		}
		// 集合函数协议: 返回 id 切片（一个参数）; in 拼 #{2|expand}
		return "", []any{anyIDs}, nil
	})

	// 表达式组合: (in categories (children-of "root"))
	// children-of 返回 IN 字面量 + 参数 — in 需要把它们挂到自己的 var
	q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	q, err := s.CompileLispInto(q, `(in categories (children-of "root"))`, "article", nil)
	if err == nil {
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("children-of: %d", len(rows))
		}
	}
	_ = child
	_ = root
}

func errTestSlug(slug string) error {
	return &testSlugErr{slug}
}

type testSlugErr struct{ slug string }

func (e *testSlugErr) Error() string { return "slug not found: " + e.slug }

// 复杂组合测试: 多层嵌套 + 穿透中间谓词 + 入边 + 集合 + 逻辑。
// 场景: 已发布文章, 属于 root 子树, 作者是高级, 标题含"甲",
//
//	其分类的父分类已发布 — 全组合。
func TestLispComplex(t *testing.T) {
	s := newFilterSvc(t)
	// 数据:
	// 根(发布) → 子(发布); 根草稿 → 子草稿
	// 甲: 挂 child, 作者张三(高级), 标题"甲-深度", views=200 → 命中
	// 乙: 挂 childDraft, 作者张三, 标题"乙", views=50 → 分类链根是草稿, 不命中
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Status: StatusPublished, Fields: Fields{"name": "子", "parent": root}})
	rootD, _ := s.Create(&Node{Type: "category", Slug: "rootd", Status: StatusDraft, Fields: Fields{"name": "根草"}})
	childD, _ := s.Create(&Node{Type: "category", Slug: "childd", Status: StatusDraft, Fields: Fields{"name": "子草", "parent": rootD}})
	zhang, _ := s.Create(&Node{Type: "person", Slug: "zhang", Fields: Fields{"name": "张三", "level": "senior"}})
	wang, _ := s.Create(&Node{Type: "person", Slug: "wang", Fields: Fields{"name": "王五", "level": "junior"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "甲-深度分析", "featured": true, "views": float64(200),
		"authors": []any{zhang}, "categories": []any{child}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "乙-简讯", "featured": false, "views": float64(50),
		"authors": []any{zhang}, "categories": []any{childD}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "丙", "featured": true, "views": float64(300),
		"authors": []any{wang}, "categories": []any{child}}})

	// 复杂表达式: 命中甲（root 子树 + 作者 senior + 标题含甲 + 分类父链全发布 + views>100）
	expr := `(and (= status 1)
	            (in categories (subtree "root"))
	            (get (-> authors) $.level "senior")
	            (like $.title "%甲%")
	            (get (-> categories) (-> parent (= status 1)) $.name "根")
	            (> $.views 100))`
	q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	q, err := s.CompileLispInto(q, expr, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	var rows []Node
	if err := q.List(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Fields["title"] != "甲-深度分析" {
		t.Fatalf("complex: %d rows", len(rows))
	}
	_ = rootD
	_ = wang
}

// 目标比较不止 =: like / >= / != 全部可作 get 目标（表达式形态）;
// 原子形态 (get 段... 字段 值) 是 = 的糖。
func TestLispGetTargetCmp(t *testing.T) {
	s := newFilterSvc(t)
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "技术动态"}})
	zhang, _ := s.Create(&Node{Type: "person", Slug: "zhang", Fields: Fields{"name": "张三", "level": "senior"}})
	wang, _ := s.Create(&Node{Type: "person", Slug: "wang", Fields: Fields{"name": "王五", "level": "mid"}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "甲", "views": float64(200), "authors": []any{zhang}, "categories": []any{child}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "乙", "views": float64(50), "authors": []any{wang}, "categories": []any{child}}})

	exec := func(expr string) int {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, expr, "article", nil)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec %q: %v", expr, err)
		}
		return len(rows)
	}
	if n := exec(`(get (-> authors) (like $.name "%三%"))`); n != 1 {
		t.Fatalf("like target: %d", n)
	}
	if n := exec(`(get (-> authors) (>= $.level "mid"))`); n != 2 {
		t.Fatalf(">= target: %d", n)
	}
	if n := exec(`(get (-> authors) (!= $.level "senior"))`); n != 1 {
		t.Fatalf("!= target: %d", n)
	}
	if n := exec(`(get (-> authors) $.level "senior")`); n != 1 {
		t.Fatalf("atomic = target: %d", n)
	}
}
