package core

import (
	"testing"
)

// Lisp filter 原型: 解析/编译/执行验证。
func TestLispFilter(t *testing.T) {
	s := newFilterSvc(t)
	// 数据: 分类树（根1 → 子2）+ 文章挂子2
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "featured": true, "categories": []any{child}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "featured": false}})

	lq := func(lisp string, params map[string]any) int {
		t.Helper()
		e, err := parseLisp(lisp)
		if err != nil {
			t.Fatalf("parse %q: %v", lisp, err)
		}
		td, _ := s.types.Type("article")
		idx := 1
		where, args, err := s.compileLisp(e, &td, params, phGen{&idx})
		if err != nil {
			t.Fatalf("compile %q: %v", lisp, err)
		}
		list, _, err := s.ListAny(where, args, 1, 10)
		if err != nil {
			t.Fatalf("exec %q: %v", lisp, err)
		}
		return len(list)
	}

	// 1. 简单比较（列 + JSON）
	if n := lq(`(= status 1)`, nil); n != 2 {
		t.Fatalf("(= status 1): %d", n)
	}
	if n := lq(`(= $.featured true)`, nil); n != 1 {
		t.Fatalf("(= $.featured true): %d", n)
	}
	// 2. 逻辑
	if n := lq(`(and (= status 1) (= $.featured true))`, nil); n != 1 {
		t.Fatalf("and: %d", n)
	}
	if n := lq(`(or (= $.featured true) (= status 1))`, nil); n != 2 {
		t.Fatalf("or: %d", n)
	}
	if n := lq(`(not (= $.featured true))`, nil); n != 1 {
		t.Fatalf("not: %d", n)
	}
	// 3. 引用 + 占位符
	if n := lq(`(ref categories {:id})`, map[string]any{"id": child}); n != 1 {
		t.Fatalf("ref: %d", n)
	}
	// 4. 集合内嵌 subtree（图原语下沉到表达式）
	if n := lq(`(in categories (subtree "root"))`, nil); n != 1 {
		t.Fatalf("in subtree: %d", n)
	}
	// 5. 多层穿透（get 路径）
	//    文章 → categories(子) → parent(根) 的 name = "根"
	//    (get categories parent $.name "根") — 3 段路径
	if n := lq(`(get categories parent $.name "根")`, nil); n != 1 {
		t.Fatalf("get 3-level: %d", n)
	}
}
