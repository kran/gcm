package migrate

import (
	"path/filepath"
	"testing"

	"github.com/kran/dba"
)

// 迁移端到端: 构造 cmx.db（categories + nodes）→ Migrate → 验证 gcm 库。
func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "cmx.db")
	dstPath := filepath.Join(dir, "gcm.db")

	// cmx.db: categories 表 + nodes 表（双表模型）
	src, err := dba.Open("sqlite", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	src.Add(`CREATE TABLE categories (id INTEGER PRIMARY KEY, parent_id INTEGER, slug TEXT, name TEXT, sort INTEGER, status INTEGER, fields TEXT, created_at TEXT)`).Exec()
	src.Add(`CREATE TABLE nodes (id INTEGER PRIMARY KEY, category_id INTEGER, slug TEXT, type TEXT, title TEXT, status INTEGER, sort INTEGER, fields TEXT, created_at TEXT, updated_at TEXT)`).Exec()
	src.Add(`INSERT INTO categories VALUES (1, NULL, 'news', '动态', 1, 1, '{}', '2024-01-01')`).Exec()
	src.Add(`INSERT INTO categories VALUES (2, 1, 'current', '时事', 2, 1, '{"subtitle":"子标题"}', '2024-01-01')`).Exec()
	src.Add(`INSERT INTO nodes VALUES (10, 2, 'a1', 'article', '文章A', 1, 1, '{"body":"x"}', '2024-01-01', '2024-01-01')`).Exec()
	src.Add(`INSERT INTO nodes VALUES (11, NULL, '', 'slide', '轮播', 1, 1, '{"h1":"标题"}', '2024-01-01', '2024-01-01')`).Exec()
	src.Close()

	types := []byte(`
types:
  category:
    title: name
    fields:
      - { name: name, kind: text }
      - { name: parent, kind: ref, to: category }
      - { name: subtitle, kind: text }
  article:
    title: title
    search: true
    fields:
      - { name: title, kind: text }
      - { name: body, kind: richtext }
      - { name: categories, kind: "ref[]", to: category }
  slide:
    title: h1
    fields:
      - { name: h1, kind: text }
`)
	st, err := Migrate(srcPath, dstPath, Options{TypeDefs: types})
	if err != nil {
		t.Fatal(err)
	}
	if st.Categories != 2 || st.Nodes != 2 || st.Edges != 2 {
		t.Fatalf("stats: %+v", st)
	}
	// 验证: 分类树（parent 边）+ 内容挂载（categories 边）+ title 投影 + FTS
	dst, err := dba.Open("sqlite", dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	var parentEdges, catEdges int
	dst.Add(`SELECT COUNT(1) FROM edges WHERE field = 'parent'`).Get(&parentEdges)
	dst.Add(`SELECT COUNT(1) FROM edges WHERE field = 'categories'`).Get(&catEdges)
	if parentEdges != 1 || catEdges != 1 {
		t.Fatalf("edges: parent=%d categories=%d", parentEdges, catEdges)
	}
	var title string
	dst.Add(`SELECT title FROM nodes WHERE slug = 'a1'`).Get(&title)
	if title != "文章A" {
		t.Fatalf("title projection: %q", title)
	}
	var fts int64
	dst.Add(`SELECT COUNT(1) FROM nodes_fts`).Get(&fts)
	if fts != 1 {
		t.Fatal("fts index missing")
	}
}
