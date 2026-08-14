package core

import (
	"path/filepath"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

// filterTypes 测试类型: 分类树 + 文章（ref/JSON/穿透场景）。
const filterTypes = `
types:
  category:
    title: name
    search: true
    fields:
      - { name: name, kind: text }
      - { name: parent, kind: ref, to: category }
  person:
    title: name
    search: true
    fields:
      - { name: name, kind: text, required: true }
      - { name: level, kind: text }
  mention:
    title: note
    fields:
      - { name: note, kind: text }
      - { name: article, kind: ref, to: article }
  comment:
    title: body
    fields:
      - { name: body, kind: text }
      - { name: article, kind: ref, to: article }
  article:
    title: title
    search: true
    fields:
      - { name: title, kind: text, required: true }
      - { name: body, kind: richtext }
      - { name: featured, kind: bool }
      - { name: views, kind: number }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
`

func newFilterSvc(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := dba.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrations.Up(db); err != nil {
		t.Fatal(err)
	}
	ts := types.New()
	if err := ts.Load([]byte(filterTypes)); err != nil {
		t.Fatal(err)
	}
	return New(db, ts)
}
