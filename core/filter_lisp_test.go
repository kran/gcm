package core

import (
	"fmt"
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
		where, args, err := s.CompileLisp(lisp, "article", params)
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
	if n := lq(`(-> categories {:id})`, map[string]any{"id": child}); n != 1 {
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

// 注册驱动: 站点自定义函数（Lisp 精髓 — 函数进表达式）。
func TestLispRegisterFunc(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "categories": []any{child}}})
	s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙"}})

	// 站点注册: (children-of "root") — 返回根分类的子树 id（集合函数）
	s.RegisterLispFunc("children-of", func(ctx *LispCtx, args []lispExpr) (string, []any, error) {
		if len(args) != 1 {
			return "", nil, fmt.Errorf("children-of takes 1 arg")
		}
		slug, _ := pathOf(args[0])
		cat, _ := ctx.svc.GetBySlug(slug)
		if cat == nil {
			return "", nil, fmt.Errorf("children-of: %q not found", slug)
		}
		ids, _ := ctx.svc.Subtree(cat.ID, "parent", 20)
		ids = append([]int64{cat.ID}, ids...)
		anyIDs := make([]any, len(ids))
		for i, id := range ids {
			anyIDs[i] = id
		}
		return "", []any{anyIDs}, nil
	})

	// 表达式组合: (in categories (children-of "root"))
	where, args, err := s.CompileLisp(`(in categories (children-of "root"))`, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListAny(where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "甲" {
		t.Fatalf("registered func: %d", len(list))
	}
}

// ^ 方向标记: 穿透的入边段（(get categories ^parent $.name "根") —
// 文章→分类(出边)→父分类(入边: 谁把该分类当 parent)→name）。
func TestLispThroughDirection(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.Create(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "根"}})
	child, _ := s.Create(&Node{Type: "category", Slug: "child", Fields: Fields{"name": "子", "parent": root}})
	// 文章挂 child（甲）; 另一篇挂 root（乙）
	art, _ := s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "甲", "categories": []any{child}}})
	b, _ := s.Create(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "乙", "categories": []any{root}}})

	// (get categories ^parent $.name "子"): 文章→分类(出边), ^parent(入边)=
	// 谁把该分类当父。乙挂 root → root 的 ^parent 入边 = child（child.parent=root）
	// → child.name="子" ✓ 乙命中; 甲挂 child → child 的 ^parent 入边 = 无 ✗
	where, args, err := s.CompileLisp(`(get categories ^parent $.name "子")`, "article", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.ListAny(where, args, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != b {
		t.Fatalf("^direction: %d (want 乙 %d)", len(list), b)
	}
	_ = art
}
