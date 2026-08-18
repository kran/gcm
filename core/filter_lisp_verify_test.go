package core

import "testing"

// B1: json_extract 缺失字段 → NULL → 比较永假（含 !=）— 语义验证
func TestVerifyB1JSONMissing(t *testing.T) {
	s := newTraverseService(t)
	// 造节点: 有 status, 无 label
	_, err := s.CreateNode(&Node{Type: "category", Status: 1, Fields: Fields{"name": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	// $label 缺失: (= $label "x") 永假
	list, _, err := s.QueryPage(ListQuery{Filter: `(= $label "x")`, Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("B1 eq: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("B1 eq: missing field should not match, got %d", len(list))
	}
	// != 也永假（NULL != "x" = NULL = false）
	list, _, err = s.QueryPage(ListQuery{Filter: `(!= $label "x")`, Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("B1 neq: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("B1 neq: missing field should not match != either, got %d", len(list))
	}
}

// B5: NOT (x = 5) 与 x != 5 对 NULL 一致
func TestVerifyB5NotNull(t *testing.T) {
	s := newTraverseService(t)
	// 造: 一个 status=5, 一个 status 缺失（列默认 0）
	if _, err := s.CreateNode(&Node{Type: "category", Status: 5, Fields: Fields{"name": "a"}}); err != nil {
		t.Fatal(err)
	}
	// NOT (= status 5) → 不含 status=5
	list, _, err := s.QueryPage(ListQuery{Filter: `(not (= status 5))`, Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("B5: %v", err)
	}
	for _, n := range list {
		if n.Status == 5 {
			t.Fatal("B5: NOT (= status 5) returned status=5")
		}
	}
}

// B4: edge 嵌套开层（e1/t1 → e2/t2）别名不冲突
func TestVerifyB4EdgeNested(t *testing.T) {
	s := newTraverseService(t)
	// 造: root → a → b（b.parent=a, a.parent=root）
	root, _ := s.CreateNode(&Node{Type: "category", Status: 1, Fields: Fields{"name": "root"}})
	a, _ := s.CreateNode(&Node{Type: "category", Status: 1, Fields: Fields{"name": "a", "parent": root}})
	b, _ := s.CreateNode(&Node{Type: "category", Status: 1, Fields: Fields{"name": "b", "parent": a}})
	// 嵌套: parent 链上 status=1 的父 → 两层 EXISTS
	list, _, err := s.QueryPage(ListQuery{
		Filter: `(edge ->parent (edge ->parent (= status 1)))`, Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("B4 nested: %v", err)
	}
	// b 的父是 a, a 的父是 root(status=1) → b 匹配
	found := false
	for _, n := range list {
		if n.ID == b {
			found = true
		}
	}
	if !found {
		t.Fatal("B4: nested edge should match b (grandparent status=1)")
	}
}

// B7: 引号字符串不支持转义 — 验证问题存在（含引号内容破坏解析）
func TestVerifyB7NoEscape(t *testing.T) {
	s := newTraverseService(t)
	// 含引号的字符串: 应该报错（无法表达）— 确认当前行为
	_, _, err := s.QueryPage(ListQuery{Filter: `(= $title "say "hi"")`, Page: 1, Size: 10})
	if err == nil {
		// 不报错说明被解析成了什么 — 查是否静默错
		t.Log("B7: 未报错（需人工确认语义）")
	} else {
		t.Logf("B7: 确认报错: %v", err)
	}
}

// B8: arrayValue 占位符缺失 → 最终仍报错（不静默）
func TestVerifyB8PlaceholderError(t *testing.T) {
	s := newTraverseService(t)
	_, _, err := s.QueryPage(ListQuery{Filter: `(in status {:missing})`, Page: 1, Size: 10})
	if err == nil {
		t.Fatal("B8: missing placeholder should error")
	}
	t.Logf("B8: 确认报错: %v", err)
}

// A3: slug 重复 — CreateNode 不查重, 撞 UNIQUE 约束（确认问题存在）
func TestVerifyA3SlugDup(t *testing.T) {
	s := newTraverseService(t)
	if _, err := s.CreateNode(&Node{Type: "category", Slug: "dup", Fields: Fields{"name": "a"}}); err != nil {
		t.Fatal(err)
	}
	// 第二个同 slug — 撞 UNIQUE — 预期报错（但错误是 SQL 层, 不是 409 语义）
	_, err := s.CreateNode(&Node{Type: "category", Slug: "dup", Fields: Fields{"name": "b"}})
	if err == nil {
		t.Fatal("A3: duplicate slug should fail")
	}
	t.Logf("A3: 确认错误: %v", err)
	// GetNodeBySlug 取到第一个（静默）
	n, err := s.GetNodeBySlug("dup")
	if err != nil || n == nil {
		t.Fatal("A3: GetNodeBySlug should return first")
	}
	if n.Fields["name"] != "a" {
		t.Fatalf("A3: got %v, want first node", n.Fields["name"])
	}
}

// A5: CreateNode 就地修改调用方 Node（Fields 剥 ref）— 确认
func TestVerifyA5MutatesInput(t *testing.T) {
	s := newTraverseService(t)
	root, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "root"}})
	n := &Node{Type: "category", Fields: Fields{"name": "child", "parent": root}}
	_, err := s.CreateNode(n)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillRef := n.Fields["parent"]; stillRef {
		t.Log("A5: 调用方 Fields 仍含 ref（未修改）")
	} else {
		t.Log("A5: 确认 — 调用方 Fields 被剥掉 ref")
	}
}

// B6: subtreeFn 编译期查库 — 确认编译时执行查询
func TestVerifyB6SubtreeAtCompile(t *testing.T) {
	s := newTraverseService(t)
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Fields: Fields{"name": "root"}})
	a, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "a", "parent": root}})
	_, err := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "b", "parent": a}})
	if err != nil {
		t.Fatal(err)
	}
	// (subtree "root") 编译期查 GetNodeBySlug + Subtree — 功能正常即确认路径
	list, _, err := s.QueryPage(ListQuery{
		Filter: `(in ->categories (subtree "root"))`, Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("B6: %v", err)
	}
	t.Logf("B6: subtree 查询正常返回 %d 条（编译期查库确认）", len(list))
}

// A1: UpdateNode 漏传 ref 字段 → 旧边被删（确认全量语义）
func TestVerifyA1FullRefReplace(t *testing.T) {
	s := newTraverseService(t)
	root, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "root"}})
	id, err := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "child", "parent": root}})
	if err != nil {
		t.Fatal(err)
	}
	// 全量更新但不带 parent 字段 → parent 边被删
	n, _ := s.GetNodeById(id)
	n.Fields = Fields{"name": "child2"} // 漏传 parent
	if err := s.UpdateNode(n); err != nil {
		t.Fatal(err)
	}
	out, _, err := s.OutEdges("category", id, "parent", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Log("A1: 确认 — 漏传 ref 字段静默删边（全量语义）")
	} else {
		t.Log("A1: 旧边保留（意外）")
	}
}

// B6c: subtree + article（专用类型定义）
const verifyTypes = `
types:
  category:
    fields:
      - { name: name, kind: textarea }
      - { name: parent, kind: ref, to: category, transitive: true, inverse: children }
      - { name: children, kind: "ref[]", to: category }
  article:
    title: title
    fields:
      - { name: title, kind: text }
      - { name: categories, kind: "ref[]", to: category }
`

func TestVerifyB6cSubtree(t *testing.T) {
	s := New(testDB(t), newTypes(t, verifyTypes))
	root, _ := s.CreateNode(&Node{Type: "category", Slug: "root", Status: 1, Fields: Fields{"name": "root"}})
	a, _ := s.CreateNode(&Node{Type: "category", Status: 1, Fields: Fields{"name": "a", "parent": root}})
	_, err := s.CreateNode(&Node{Type: "article", Status: 1, Fields: Fields{"title": "art1", "categories": []any{a}}})
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.QueryPage(ListQuery{
		Filter: `(in ->categories (subtree "root"))`, Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("B6c: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("B6c: want 1 article, got %d", len(list))
	}
	t.Log("B6c: subtree 编译期查库 + 结果正确")
}
