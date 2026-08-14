package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

// 测试用类型定义: 文章↔专家 + 分类树 + 关系节点。
const testTypes = `
types:
  article:
    fields:
      - { name: body, kind: richtext, required: true }
      - { name: cover, kind: upload-image }
      - { name: authors, kind: "ref[]", to: person, inverse: articles }
  person:
    fields:
      - { name: name, kind: text, required: true }
      - { name: articles, kind: "ref[]", to: article, inverse: authors }
  category:
    fields:
      - { name: name, kind: text, required: true }
      - { name: parent, kind: ref, to: category }
`

// testDB 临时库 + 迁移。
func testDB(t *testing.T) *dba.SQL {
	t.Helper()
	db, err := dba.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newService(t *testing.T) *Service {
	t.Helper()
	return New(testDB(t), newTypes(t, testTypes))
}

// newTypes 独立类型容器（自定义类型定义用）。
func newTypes(t *testing.T, yaml string) *types.Types {
	t.Helper()
	ts := types.New()
	if err := ts.Load([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestCreateGet(t *testing.T) {
	s := newService(t)
	id, err := s.CreateNode(&Node{Type: "person", Slug: "li-zhiqi", Fields: Fields{"name": "李志起"}})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	n, err := s.GetNodeById(id)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "person" || n.Slug != "li-zhiqi" || n.Status != StatusDraft {
		t.Fatalf("node: %+v", n)
	}
	if n.Fields["name"] != "李志起" {
		t.Fatalf("fields: %v", n.Fields)
	}
}

func TestCreateRefs(t *testing.T) {
	s := newService(t)
	pid, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	// ref 字段: authors 落 edges
	aid, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"body": "<p>hi</p>", "authors": []any{pid}}})
	if err != nil {
		t.Fatalf("CreateNode with ref: %v", err)
	}
	// 节点 fields 不含 ref
	n, _ := s.GetNodeById(aid)
	if _, ok := n.Fields["authors"]; ok {
		t.Fatal("ref field must not be in node fields")
	}
	// 边已落
	cnt := countEdges(t, s, aid)
	if cnt != 1 {
		t.Fatalf("edges: %d, want 1", cnt)
	}
}

func TestCreateRefValidation(t *testing.T) {
	s := newService(t)
	// ref 目标不存在
	if _, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"body": "x", "authors": []any{999}}}); err == nil {
		t.Fatal("missing target must fail")
	}
	// ref 目标类型不匹配 (person 的 articles 指向 article, 传 category id)
	catID, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "c"}})
	if _, err := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "p", "articles": []any{catID}}}); err == nil {
		t.Fatal("type mismatch must fail")
	}
}

func TestCreateValidation(t *testing.T) {
	s := newService(t)
	// 未知字段
	if _, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"body": "x", "ghost": 1}}); err == nil {
		t.Fatal("unknown field must fail")
	}
	// required 缺失
	if _, err := s.CreateNode(&Node{Type: "article", Fields: Fields{}}); err == nil {
		t.Fatal("required missing must fail")
	}
	// 未知类型
	if _, err := s.CreateNode(&Node{Type: "ghost", Fields: Fields{}}); err == nil {
		t.Fatal("unknown type must fail")
	}
}

func TestSlugUnique(t *testing.T) {
	s := newService(t)
	if _, err := s.CreateNode(&Node{Type: "person", Slug: "same", Fields: Fields{"name": "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNode(&Node{Type: "person", Slug: "same", Fields: Fields{"name": "b"}}); err == nil {
		t.Fatal("duplicate slug must fail")
	}
	// 空 slug 多个共存
	if _, err := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "c"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "d"}}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReplacesRefs(t *testing.T) {
	s := newService(t)
	p1, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	p2, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "李四"}})
	aid, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"body": "v1", "authors": []any{p1}}})
	if err != nil {
		t.Fatal(err)
	}
	// 全量更新: 替换 authors
	if err := s.UpdateNode(&Node{ID: aid, Type: "article", Fields: Fields{"body": "v2", "authors": []any{p2}}}); err != nil {
		t.Fatal(err)
	}
	if countEdges(t, s, aid) != 1 {
		t.Fatal("edges must be replaced, not appended")
	}
	// 清空 authors
	if err := s.UpdateNode(&Node{ID: aid, Type: "article", Fields: Fields{"body": "v3"}}); err != nil {
		t.Fatal(err)
	}
	if countEdges(t, s, aid) != 0 {
		t.Fatal("edges must be cleared")
	}
	n, _ := s.GetNodeById(aid)
	if n.Fields["body"] != "v3" {
		t.Fatalf("body not updated: %v", n.Fields)
	}
}

func TestDeleteCascadesRefs(t *testing.T) {
	s := newService(t)
	person, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	aid, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"body": "x", "authors": []any{person}}})
	// 删 person: 它的入边（article.authors → person）清掉
	if err := s.DeleteNode(person); err != nil {
		t.Fatal(err)
	}
	if countEdges(t, s, aid) != 0 {
		t.Fatal("delete must cascade inbound refs")
	}
	if err := s.DeleteNode(aid); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(999); err == nil {
		t.Fatal("delete missing must fail")
	}
}

func TestListFilter(t *testing.T) {
	s := newService(t)
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Sort: 1, Fields: Fields{"body": "a"}})
	s.CreateNode(&Node{Type: "article", Status: StatusDraft, Sort: 2, Fields: Fields{"body": "b"}})
	s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "p"}})

	list, total, err := s.List("article", StatusPublished, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Fields["body"] != "a" {
		t.Fatalf("published: %d %d %v", total, len(list), list)
	}
	// 分页: draft 1 条
	list, total, _ = s.List("article", StatusDraft, 1, 10)
	if total != 1 {
		t.Fatalf("draft total: %d", total)
	}
	// 不过滤
	_, total, _ = s.List("article", -1, 1, 10)
	if total != 2 {
		t.Fatalf("all total: %d", total)
	}
	// 按类型隔离
	_, total, _ = s.List("person", -1, 1, 10)
	if total != 1 {
		t.Fatalf("person total: %d", total)
	}
}

// countEdges 查节点出边数（测试辅助）。
func countEdges(t *testing.T, s *Service, from int64) int {
	t.Helper()
	var cnt int64
	_, err := s.db.Add(
		`SELECT COUNT(1) FROM edges WHERE from_node = #{1}`, from).Get(&cnt)
	if err != nil {
		t.Fatal(err)
	}
	return int(cnt)
}

// Title 列: 类型 title 声明映射（双存: fields 保留 + 列投影）。
func TestTitleColumn(t *testing.T) {
	// 带 title 声明的类型定义
	ts := newTypes(t, `
types:
  article:
    title: title
    fields:
      - { name: title, kind: text, required: true }
      - { name: body, kind: richtext }
  person:
    title: name
    fields:
      - { name: name, kind: text, required: true }
  org:
    fields:
      - { name: name, kind: text }
`)
	s := New(testDB(t), ts)
	aid, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "标题甲", "body": "x"}})
	n, _ := s.GetNodeById(aid)
	if n.Title != "标题甲" {
		t.Fatalf("title column: %q", n.Title)
	}
	if n.Fields["title"] != "标题甲" {
		t.Fatalf("fields title kept: %v", n.Fields)
	}
	// person → name 映射
	pid, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	n, _ = s.GetNodeById(pid)
	if n.Title != "张三" {
		t.Fatalf("person title: %q", n.Title)
	}
	// 无声明类型 → 空列
	oid, _ := s.CreateNode(&Node{Type: "org", Fields: Fields{"name": "机构"}})
	n, _ = s.GetNodeById(oid)
	if n.Title != "" {
		t.Fatalf("org title must be empty: %q", n.Title)
	}
	// UpdateNode 同步
	s.UpdateNode(&Node{ID: aid, Type: "article", Fields: Fields{"title": "标题乙", "body": "y"}})
	n, _ = s.GetNodeById(aid)
	if n.Title != "标题乙" {
		t.Fatalf("update title: %q", n.Title)
	}
}

// FullFields 超 1000 引用不全量截断（回归: 曾硬编码 OutEdges 1,1000）。
func TestFullFieldsNoTruncate(t *testing.T) {
	s := newFilterSvc(t)
	// 1 篇 1500 引用的文章（绕过 ValidateFields 用裸 ref 数组? — CreateNode 校验数量）
	// 直接建 1500 个 category + 1 篇文章挂 1500 引用
	ids := make([]any, 1500)
	for i := range ids {
		id, err := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "c" + strconv.Itoa(i)}})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	aid, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "big", "categories": ids}})
	if err != nil {
		t.Fatal(err)
	}
	ff, err := s.FullFields(aid)
	if err != nil {
		t.Fatal(err)
	}
	cats, ok := ff["categories"].([]any)
	if !ok || len(cats) != 1500 {
		t.Fatalf("FullFields must return all 1500 refs, got %d (%T)", len(cats), ff["categories"])
	}
}

// UpdateNode 不就地修改调用方 Node（无隐蔽副作用）。
func TestUpdateNoSideEffect(t *testing.T) {
	s := newFilterSvc(t)
	id, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "t1", "categories": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{ID: id, Type: "article", Title: "t2", Fields: Fields{"title": "t2", "categories": []any{}}}
	if err := s.UpdateNode(n); err != nil {
		t.Fatal(err)
	}
	// 调用方视图: Fields 不被剥、Title 不被覆盖、UpdatedAt 不被碰
	if _, hasCat := n.Fields["categories"]; !hasCat {
		t.Fatal("UpdateNode must not strip caller's Fields")
	}
	if n.Title != "t2" {
		t.Fatalf("UpdateNode must not overwrite caller's Title: %q", n.Title)
	}
}

// title 穿透: employment 的 title 列 = 引用 person 的 name（写时快照）。
func TestTitleThroughProjection(t *testing.T) {
	s := New(testDB(t), newTypes(t, `
types:
  person:
    title: name
    fields:
      - { name: name, kind: text }
  employment:
    title: person.$.name
    fields:
      - { name: person, kind: ref, to: person }
      - { name: role, kind: text }
`))
	li, err := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "李志起"}})
	if err != nil {
		t.Fatal(err)
	}
	emp, err := s.CreateNode(&Node{Type: "employment", Fields: Fields{"person": li, "role": "理事长"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNodeById(emp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "李志起" {
		t.Fatalf("title through: want 李志起, got %q", got.Title)
	}
	// 无引用 → 空（不崩）
	emp2, err := s.CreateNode(&Node{Type: "employment", Fields: Fields{"role": "顾问"}})
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetNodeById(emp2)
	if got2.Title != "" {
		t.Fatalf("title through without ref: want empty, got %q", got2.Title)
	}
}

// hook 接线: 标准事件触发（Save 带节点 / DeleteNode 带 id）+ 失败回滚。
func TestServiceHooks(t *testing.T) {
	s := newFilterSvc(t)
	var saved, deleted []string
	s.Hooks().AddHook(HookNodeSave, func(n *Node) error {
		saved = append(saved, n.Type+":"+n.Title)
		return nil
	})
	s.Hooks().AddHook(HookNodeDelete, func(id int64) error {
		deleted = append(deleted, fmt.Sprintf("%d", id))
		return nil
	})
	id, err := s.CreateNode(&Node{Type: "article", Title: "钩子测试",
		Fields: Fields{"title": "钩子测试", "body": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0] != "article:钩子测试" {
		t.Fatalf("save hook: %v", saved)
	}
	n, _ := s.GetNodeById(id)
	n.Fields["title"] = "改名"
	if err := s.UpdateNode(n); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("update must fire save hook: %v", saved)
	}
	if err := s.DeleteNode(id); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != fmt.Sprintf("%d", id) {
		t.Fatalf("delete hook: %v", deleted)
	}
}

// hook 失败 → 事务回滚（fail-loud: 节点不落库）。
func TestHookAbortRollsBack(t *testing.T) {
	s := newFilterSvc(t)
	s.Hooks().AddHook(HookNodeSave, func(n *Node) error {
		return errors.New("hook rejects")
	})
	id, err := s.CreateNode(&Node{Type: "article", Title: "x", Fields: Fields{"title": "x"}})
	if err == nil || !strings.Contains(err.Error(), "hook rejects") {
		t.Fatalf("must fail with hook error, got %v", err)
	}
	got, _ := s.GetNodeById(id)
	if got != nil {
		t.Fatal("node must not persist when hook fails")
	}
}

// slug 写入期约束: 非法 slug 拒绝（fail-loud）。
func TestSlugConstraint(t *testing.T) {
	s := newFilterSvc(t)
	if _, err := s.CreateNode(&Node{Type: "article", Slug: "a--b", Fields: Fields{"title": "x"}}); err == nil {
		t.Fatal("slug a--b must be rejected")
	}
	if _, err := s.CreateNode(&Node{Type: "article", Slug: "1abc", Fields: Fields{"title": "x"}}); err == nil {
		t.Fatal("slug starting with digit must be rejected")
	}
	id, err := s.CreateNode(&Node{Type: "article", Slug: "good-slug", Fields: Fields{"title": "x"}})
	if err != nil {
		t.Fatalf("valid slug: %v", err)
	}
	n, _ := s.GetNodeById(id)
	n.Slug = "bad--slug"
	if err := s.UpdateNode(n); err == nil {
		t.Fatal("update with bad slug must be rejected")
	}
}
