package core

import (
	"testing"
)

// var 链编译器（CompileLispV）: 与旧编译器（CompileLisp）结果一致。
func TestLispCompileV(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Status: StatusPublished, Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "featured": true, "categories": []any{child}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "featured": false}})

	check := func(lisp string, want int) {
		t.Helper()
		where, args, err := s.CompileLispV(lisp, "article", nil)
		if err != nil {
			t.Fatalf("compile %q: %v", lisp, err)
		}
		list, _, err := s.ListAny(where, args, 1, 10)
		if err != nil {
			t.Fatalf("exec %q: %v", lisp, err)
		}
		if len(list) != want {
			t.Fatalf("%q: want %d got %d (where=%s)", lisp, want, len(list), where)
		}
	}

	check(`(and (= type "article") (= status 1))`, 2)
	check(`(= $.featured true)`, 1)
	check(`(and (= status 1) (= $.featured true))`, 1)
	check(`(not (= $.featured true))`, 1)
	// 占位符（需 params）:
	where, args, err := s.CompileLispV(`(-> categories {:id})`, "article", map[string]any{"id": child})
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListAny(where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("placeholder ref: %d", len(list))
	}
	check(`(in categories (subtree "root"))`, 1)
	check(`(get (-> categories) (-> parent) $.name "根")`, 1)
	// 中间条件
	check(`(get (-> categories) (-> parent (= status 1)) $.name "根")`, 1)
}
