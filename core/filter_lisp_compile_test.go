package core

import (
	"testing"
)

// var 链编译器（CompileLispV）: 与旧编译器（CompileLisp）结果一致。
func TestLispCompileV(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "featured": true, "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "featured": false}})

	// 挂载模式: base SQL 模板（${where} 槽）+ CompileLispInto + 执行
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
		rows := exec(lisp, nil)
		if len(rows) != want {
			t.Fatalf("%q: want %d got %d", lisp, want, len(rows))
		}
	}

	check(`(and (= type "article") (= status 1))`, 2)
	check(`(= $featured true)`, 1)
	check(`(and (= status 1) (= $featured true))`, 1)
	check(`(not (= $featured true))`, 1)
	// 占位符
	rows := exec(`(edge ->categories {:id})`, map[string]any{"id": child})
	if len(rows) != 1 {
		t.Fatalf("placeholder ref: %d", len(rows))
	}
	check(`(in ->categories (subtree "root"))`, 1)
	check(`(edge ->categories (edge ->parent (= $name "根")))`, 1)
	check(`(edge ->categories (edge ->parent (and (= status 1) (= $name "根"))))`, 1)
}

// Q 结构化查询: base SQL 模板 + ${where} 槽（Lisp 编译挂载）+ 分页/排序。
func TestLispListQ(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Sort: 1, Fields: Fields{"title": "甲", "categories": []any{child}}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Sort: 2, Fields: Fields{"title": "乙", "categories": []any{root}}})

	// Filter（Lisp）+ 分页
	list, total, err := s.Q(ListQuery{Filter: `(and (= type "article") (in ->categories (subtree "root")))`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("Q: total=%d len=%d", total, len(list))
	}
	// Sort + 分页
	list, _, err = s.Q(ListQuery{Filter: `(and (= type "article") (= status 1))`, Sort: "sort DESC", Page: 1, Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "乙" {
		t.Fatalf("sort: %v", list)
	}
	// 空 filter
	list, total, err = s.Q(ListQuery{Page: 1, Size: 10})
	if err != nil || total == 0 {
		t.Fatalf("no filter: %v", err)
	}
}
