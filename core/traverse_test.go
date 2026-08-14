package core

import (
	"reflect"
	"testing"
)

// 树 + 等价类类型定义。
const traverseTypes = `
types:
  category:
    fields:
      - { name: name, kind: textarea }
      - { name: parent, kind: ref, to: category, transitive: true, inverse: children }
      - { name: children, kind: "ref[]", to: category }
      - { name: synonym, kind: "ref[]", to: category, equivalence: true }
`

func newTraverseService(t *testing.T) *Service {
	t.Helper()
	return New(testDB(t), newTypes(t, traverseTypes))
}

// 造树: root → a → b（b.parent=a, a.parent=root）
func buildTree(t *testing.T, s *Service) (root, a, b int64) {
	t.Helper()
	root, _ = s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "root"}})
	a, _ = s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "a", "parent": root}})
	b, _ = s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "b", "parent": a}})
	return
}

func TestTraverseUp(t *testing.T) {
	s := newTraverseService(t)
	root, a, b := buildTree(t, s)

	// b 向上: [a, root]
	got, err := s.Traverse(b, "parent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{root, a}) { // ORDER BY id
		t.Fatalf("traverse b: %v", got)
	}
	// a 向上: [root]
	got, _ = s.Traverse(a, "parent", 10)
	if !reflect.DeepEqual(got, []int64{root}) {
		t.Fatalf("traverse a: %v", got)
	}
	// root 向上: 空
	got, _ = s.Traverse(root, "parent", 10)
	if len(got) != 0 {
		t.Fatalf("traverse root: %v", got)
	}
}

func TestSubtreeDown(t *testing.T) {
	s := newTraverseService(t)
	root, a, b := buildTree(t, s)

	got, err := s.Subtree(root, "parent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{a, b}) {
		t.Fatalf("subtree root: %v", got)
	}
	// a 的子树: [b]
	got, _ = s.Subtree(a, "parent", 10)
	if !reflect.DeepEqual(got, []int64{b}) {
		t.Fatalf("subtree a: %v", got)
	}
	// b 无子树
	got, _ = s.Subtree(b, "parent", 10)
	if len(got) != 0 {
		t.Fatalf("subtree b: %v", got)
	}
}

func TestTraverseMaxHops(t *testing.T) {
	s := newTraverseService(t)
	_, a, b := buildTree(t, s)

	// 1 跳: 只到 a
	got, _ := s.Traverse(b, "parent", 1)
	if !reflect.DeepEqual(got, []int64{a}) {
		t.Fatalf("1 hop: %v", got)
	}
	// 子树 1 跳: root 只到 a
	got, _ = s.Subtree(a, "parent", 1)
	if !reflect.DeepEqual(got, []int64{b}) {
		t.Fatalf("subtree 1 hop: %v", got)
	}
}

func TestEquivalenceClass(t *testing.T) {
	s := newTraverseService(t)
	// 等价类: x ↔ y ↔ z（只存单向边 x→y, y→z — 等价无方向, 类内全可达）
	x, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "x"}})
	y, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "y"}})
	z, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "z"}})
	alone, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "alone"}})
	s.AddEdge(x, y, "synonym", 0)
	s.AddEdge(y, z, "synonym", 0)

	got, err := s.EquivalenceClass(y, "synonym", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{x, y, z}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("class y: %v, want %v", got, want)
	}
	// 孤立节点: 只有自己
	got, _ = s.EquivalenceClass(alone, "synonym", 10)
	if !reflect.DeepEqual(got, []int64{alone}) {
		t.Fatalf("class alone: %v", got)
	}
}

// 环防: 循环引用不无限递归, maxHops 截断。
func TestTraverseCycle(t *testing.T) {
	s := newTraverseService(t)
	a, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "a"}})
	b, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "b"}})
	s.AddEdge(a, b, "parent", 0)
	s.AddEdge(b, a, "parent", 0) // 环

	got, err := s.Traverse(a, "parent", 5)
	if err != nil {
		t.Fatal(err)
	}
	// 5 跳内: b, a(第二跳), b(第三跳)... DISTINCT → [a, b]
	if len(got) != 2 {
		t.Fatalf("cycle traverse: %v", got)
	}
	// 等价类（双向遍历）在环上也安全: 用 parent 字段双向展开
	got, err = s.EquivalenceClass(a, "parent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("cycle class: %v", got)
	}
}

// 非引用字段/未知字段遍历 fail-loud。
func TestTraverseValidation(t *testing.T) {
	s := newTraverseService(t)
	root, _, _ := buildTree(t, s)
	if _, err := s.Traverse(root, "ghost", 10); err == nil {
		t.Fatal("unknown field must fail")
	}
	if _, err := s.Traverse(root, "name", 10); err == nil {
		t.Fatal("non-ref field must fail")
	}
	if _, err := s.Subtree(999, "parent", 10); err == nil {
		t.Fatal("missing start must fail")
	}
}
