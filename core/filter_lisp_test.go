package core

import (
	"testing"
)

// Lisp filter（var 槽版）: 解析/编译/执行。
func TestLispFilter(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "featured": true, "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "featured": false}})

	exec := func(lisp string, params map[string]any) []Node {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, lisp, params)
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
	check(`(= $featured true)`, 1)
	check(`(and (= status 1) (= $featured true))`, 1)
	check(`(not (= $featured true))`, 1)
	rows := exec(`(edge ->categories {:id})`, map[string]any{"id": child})
	if len(rows) != 1 {
		t.Fatalf("placeholder ref: %d", len(rows))
	}
	check(`(in ->categories (subtree "root"))`, 1)
	check(`(edge ->categories (edge ->parent (= $name "根")))`, 1)
	check(`(edge ->categories (edge ->parent (and (= status 1) (= $name "根"))))`, 1)
}

// 注册驱动: 站点自定义函数（Lisp 精髓 — 函数进表达式）。
func TestLispRegisterFunc(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "categories": []any{child}}})

	// 站点注册: (children-of "root") → id 列表（直接参数）
	s.RegisterLispFuncC("children-of", func(args []lispExpr) (string, []any, error) {
		slug, _ := pathOfC(args[0])
		cat, _ := s.GetNodeBySlug(slug)
		if cat == nil {
			return "", nil, errTestSlug(slug)
		}
		ids, _ := s.Subtree(cat.Type, cat.ID, "parent", 20)
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
	q, err := s.CompileLispInto(q, `(in categories (children-of "root"))`, nil)
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
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Status: StatusPublished, Fields: Fields{"name": "子", "parent": root}})
	rootD, _ := s.CreateNode(&Node{Type: "category", Slug: "rootd", Status: StatusDraft, Fields: Fields{"name": "根草"}})
	childD, _ := s.CreateNode(&Node{Type: "category", Slug: "childd", Status: StatusDraft, Fields: Fields{"name": "子草", "parent": rootD}})
	zhang, _ := s.CreateNode(&Node{Type: "person", Slug: "zhang", Fields: Fields{"name": "张三", "level": "senior"}})
	wang, _ := s.CreateNode(&Node{Type: "person", Slug: "wang", Fields: Fields{"name": "王五", "level": "junior"}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "甲-深度分析", "featured": true, "views": float64(200),
		"authors": []any{zhang}, "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "乙-简讯", "featured": false, "views": float64(50),
		"authors": []any{zhang}, "categories": []any{childD}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "丙", "featured": true, "views": float64(300),
		"authors": []any{wang}, "categories": []any{child}}})

	// 复杂表达式: 命中甲（root 子树 + 作者 senior + 标题含甲 + 分类父链全发布 + views>100）
	expr := `(and (= status 1)
	            (in ->categories (subtree "root"))
	            (edge ->authors (= $level "senior"))
	            (like $title "%甲%")
	            (edge ->categories (edge ->parent (and (= status 1) (= $name "根"))))
	            (> $views 100))`
	q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	q, err := s.CompileLispInto(q, expr, nil)
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
// edge 目标比较表达式形态（like/>=/!= 全支持）。
func TestLispGetTargetCmp(t *testing.T) {
	s := newFilterSvc(t)
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "技术动态"}})
	zhang, _ := s.CreateNode(&Node{Type: "person", Slug: "zhang", Fields: Fields{"name": "张三", "level": "senior"}})
	wang, _ := s.CreateNode(&Node{Type: "person", Slug: "wang", Fields: Fields{"name": "王五", "level": "mid"}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "甲", "views": float64(200), "authors": []any{zhang}, "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{
		"title": "乙", "views": float64(50), "authors": []any{wang}, "categories": []any{child}}})

	exec := func(expr string) int {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, expr, nil)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec %q: %v", expr, err)
		}
		return len(rows)
	}
	if n := exec(`(edge ->authors (like $name "%三%"))`); n != 1 {
		t.Fatalf("like target: %d", n)
	}
	if n := exec(`(edge ->authors (>= $level "mid"))`); n != 2 {
		t.Fatalf(">= target: %d", n)
	}
	if n := exec(`(edge ->authors (!= $level "senior"))`); n != 1 {
		t.Fatalf("!= target: %d", n)
	}
	if n := exec(`(edge ->authors (= $level "senior"))`); n != 1 {
		t.Fatalf("atomic = target: %d", n)
	}
}

// 占位符数组形态: (in ->categories {:ids}) — params 绑定 id 数组。
func TestLispInPlaceholderArray(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	other, _ := s.CreateNode(&Node{Type: "category", Slug: "other", Fields: Fields{"name": "旁"}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "categories": []any{other}}})

	exec := func(ids []any) int {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, `(in ->categories {:ids})`, map[string]any{"ids": ids})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec: %v", err)
		}
		return len(rows)
	}
	if n := exec([]any{child}); n != 1 {
		t.Fatalf("单 id: %d", n)
	}
	if n := exec([]any{child, other}); n != 2 {
		t.Fatalf("双 id: %d", n)
	}
	if n := exec([]any{root}); n != 0 {
		t.Fatalf("无关 id: %d", n)
	}
}

// 新语法专项: 数组字面量 / 标量 IN / 入边 / 空格字符串 / 空数组。
func TestLispNewSyntax(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Status: StatusPublished, Fields: Fields{"name": "子", "parent": root}})
	zhang, _ := s.CreateNode(&Node{Type: "person", Slug: "zhang", Fields: Fields{"name": "张三"}})
	aid, _ := s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "hello world 甲", "featured": true, "views": float64(200), "authors": []any{zhang}, "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "featured": false, "views": float64(50)}})
	// 评论（入边场景: comment.article ref 字段 → 文章, 引擎自动落边）
	_, _ = s.CreateNode(&Node{Type: "comment", Fields: Fields{"body": "好评", "article": aid}})

	exec := func(expr string) int {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, expr, nil)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec %q: %v", expr, err)
		}
		return len(rows)
	}
	// 空格字符串（引号整段读取）
	if n := exec(`(like $title "%hello world%")`); n != 1 {
		t.Fatalf("空格字符串: %d", n)
	}
	// 数组字面量 + 标量 IN（全库 status=1: 2 文章 + root/child 分类）
	if n := exec(`(in status [1 1])`); n != 4 {
		t.Fatalf("标量 IN 数组: %d", n)
	}
	// 字面量数组 + 引用字段
	if n := exec(`(in ->categories [2 999])`); n != 1 {
		t.Fatalf("引用 IN 数组: %d", n)
	}
	// 空数组 → 永假
	if n := exec(`(in status [])`); n != 0 {
		t.Fatalf("空数组: %d", n)
	}
	// 入边存在性
	if n := exec(`(edge <-comment.article)`); n != 1 {
		t.Fatalf("入边存在: %d", n)
	}
	// 入边目标谓词
	if n := exec(`(edge <-comment.article (= $body "好评"))`); n != 1 {
		t.Fatalf("入边谓词: %d", n)
	}
	// edge 数组目标 → 报错
	q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	if _, err := s.CompileLispInto(q, `(edge ->categories [1 2])`, nil); err == nil {
		t.Fatal("edge 数组目标应报错")
	}
	// 引用字段直接比较 → 报错
	q = s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	if _, err := s.CompileLispInto(q, `(= ->categories {:id})`, map[string]any{"id": child}); err == nil {
		t.Fatal("引用字段直接比较应报错")
	}
	// $.name 严格拒绝（不兼容）
	q = s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	if _, err := s.CompileLispInto(q, `(= $.featured true)`, nil); err == nil {
		t.Fatal("$. 前缀应报错（严格 $name）")
	}
}

// 入边歧义消除: 两个类型声明同名 ref 字段 — <-type.field 显式区分。
func TestLispInEdgeTypeDisambiguate(t *testing.T) {
	s := newFilterSvc(t)
	aid, _ := s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲"}})
	// comment 和 mention 都声明 article ref to article
	s.CreateNode(&Node{Type: "comment", Fields: Fields{"body": "评论", "article": aid}})
	s.CreateNode(&Node{Type: "mention", Fields: Fields{"note": "提及", "article": aid}})

	exec := func(expr string) int {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, expr, nil)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec %q: %v", expr, err)
		}
		return len(rows)
	}
	// 裸 <-article 拒绝（身份不完整）
	q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	if _, err := s.CompileLispInto(q, `(edge <-article)`, nil); err == nil {
		t.Fatal("裸入边应报错")
	}
	// 类型限定: 各自命中
	if n := exec(`(edge <-comment.article)`); n != 1 {
		t.Fatalf("comment 入边: %d", n)
	}
	if n := exec(`(edge <-mention.article)`); n != 1 {
		t.Fatalf("mention 入边: %d", n)
	}
	// 谓词在各自类型上编译
	if n := exec(`(edge <-comment.article (= $body "评论"))`); n != 1 {
		t.Fatalf("comment 谓词: %d", n)
	}

}

// 通配入边: (edge <-*) 任意来源存在性; (in <-* {:ids}) 来源在集合。
func TestLispWildcardIn(t *testing.T) {
	s := newFilterSvc(t)
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子"}})
	aid, _ := s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "categories": []any{child}}})
	// 评论也引用 article（comment.article 边）
	_, _ = s.CreateNode(&Node{Type: "comment", Fields: Fields{"body": "c", "article": aid}})

	exec := func(expr string, params map[string]any) int {
		t.Helper()
		q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
		q, err := s.CompileLispInto(q, expr, params)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		var rows []Node
		if err := q.List(&rows); err != nil {
			t.Fatalf("exec %q: %v", expr, err)
		}
		return len(rows)
	}
	// 有入边的节点: child(被 article 的 categories 边) + article(被 comment 的 article 边) = 2
	if n := exec(`(edge <-*)`, nil); n != 2 {
		t.Fatalf("<-* 存在性: %d", n)
	}
	// 通配入边 + 目标谓词 → 报错
	q := s.db.Add(`SELECT * FROM nodes WHERE ${where}`)
	if _, err := s.CompileLispInto(q, `(edge <-* (= $x 1))`, nil); err == nil {
		t.Fatal("<-* 带目标谓词应报错")
	}
	// in <-* 通配集合: 来源在 [aid]（article 有 categories/authors 出边 → child+zhang 被引用）
	if n := exec(`(in <-* {:ids})`, map[string]any{"ids": []any{aid}}); n != 2 {
		t.Fatalf("in <-* : %d", n)
	}
}
