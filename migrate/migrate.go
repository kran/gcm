// Package migrate cmx → gcm 数据迁移工具。
//
// cmx 是双表模型（nodes + categories）; gcm 是单表 + edges:
//
//	cmx categories        → gcm nodes(type=category); parent_id → edges(parent)
//	cmx nodes.category_id → gcm edges(field=categories, from=node, to=分类)
//
// 类型定义驱动: 未知字段过滤（cmx 校验松散, gcm 严格）; title 列只在类型
// 声明 title 字段时写回（投影自动处理）; FTS 索引由 CreateNode 自动同步。
package migrate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

// Options 迁移选项。
type Options struct {
	TypeDefs      []byte // gcm types.yaml（字段过滤/校验的依据）
	CategoryType  string // 分类类型名（默认 "category"）
	CategoryField string // 内容挂载分类的 ref 字段名（默认 "categories"）
	ParentField   string // 分类父引用 ref 字段名（默认 "parent"）
}

// Stats 迁移统计。
type Stats struct {
	Categories int
	Nodes      int
	Edges      int
}

// Migrate 读 cmx.db（srcPath）→ 写 gcm 库（dstPath）。目标库已存在则覆盖。
func Migrate(srcPath, dstPath string, opts Options) (*Stats, error) {
	if opts.CategoryType == "" {
		opts.CategoryType = "category"
	}
	if opts.CategoryField == "" {
		opts.CategoryField = "categories"
	}
	if opts.ParentField == "" {
		opts.ParentField = "parent"
	}
	src, err := sql.Open("sqlite", srcPath)
	if err != nil {
		return nil, fmt.Errorf("migrate: open src: %w", err)
	}
	defer src.Close()

	os.Remove(dstPath)
	db, err := dba.Open("sqlite", dstPath)
	if err != nil {
		return nil, fmt.Errorf("migrate: open dst: %w", err)
	}
	if err := migrations.Up(db); err != nil {
		return nil, err
	}
	ts := types.New()
	if len(opts.TypeDefs) > 0 {
		if err := ts.Load(opts.TypeDefs); err != nil {
			return nil, fmt.Errorf("migrate: types: %w", err)
		}
	}
	svc := core.New(db, ts)
	m := &migrator{svc: svc, ts: ts, opts: opts}
	return m.run(src)
}

type migrator struct {
	svc  *core.Service
	ts   *types.Types
	opts Options
}

func (m *migrator) run(src *sql.DB) (*Stats, error) {
	st := &Stats{}

	// 1. 分类（cmx categories → gcm nodes type=category; parent_id → parent 边）
	type catRow struct {
		id, parent                  int64
		hasParent                   bool
		slug, name, fields, created string
		sort, status                int
	}
	rows, err := src.Query(`SELECT id, parent_id, slug, name, sort, status, fields, created_at FROM categories ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read categories: %w", err)
	}
	var cats []catRow
	for rows.Next() {
		var c catRow
		var parent sql.NullInt64
		var slug, fields, created sql.NullString
		if err := rows.Scan(&c.id, &parent, &slug, &c.name, &c.sort, &c.status, &fields, &created); err != nil {
			return nil, err
		}
		c.hasParent = parent.Valid
		c.parent = parent.Int64
		c.slug, c.fields, c.created = slug.String, fields.String, created.String
		cats = append(cats, c)
	}
	rows.Close()

	catID := map[int64]int64{}
	for _, c := range cats {
		ff := m.filterFields(m.opts.CategoryType, parseFields(c.fields))
		ff["name"] = c.name
		id, err := m.svc.CreateNode(&core.Node{Type: m.opts.CategoryType, Slug: c.slug,
			Status: c.status, Sort: c.sort, CreatedAt: parseTime(c.created), Fields: ff})
		if err != nil {
			return nil, fmt.Errorf("migrate: category %s: %w", c.name, err)
		}
		catID[c.id] = id
		st.Categories++
	}
	for _, c := range cats {
		if c.hasParent {
			if pid, ok := catID[c.parent]; ok {
				if _, err := m.svc.AddEdge(catID[c.id], pid, m.opts.ParentField, c.sort); err != nil {
					return nil, fmt.Errorf("migrate: category parent %d: %w", c.id, err)
				}
				st.Edges++
			}
		}
	}

	// 2. 内容节点（cmx nodes → gcm nodes; category_id → categories 边）
	nrows, err := src.Query(`SELECT id, category_id, slug, type, title, status, sort, fields, created_at, updated_at FROM nodes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read nodes: %w", err)
	}
	defer nrows.Close()
	for nrows.Next() {
		var id int64
		var catIDn sql.NullInt64
		var slug, typ, title, fields, created, updated sql.NullString
		var status, sort int
		if err := nrows.Scan(&id, &catIDn, &slug, &typ, &title, &status, &sort, &fields, &created, &updated); err != nil {
			return nil, err
		}
		ff := m.filterFields(typ.String, parseFields(fields.String))
		if title.String != "" && m.hasField(typ.String, "title") {
			ff["title"] = title.String
		}
		newID, err := m.svc.CreateNode(&core.Node{Type: typ.String, Slug: slug.String,
			Status: status, Sort: sort, CreatedAt: parseTime(created.String),
			UpdatedAt: parseTime(updated.String), Fields: ff})
		if err != nil {
			return nil, fmt.Errorf("migrate: node %d (%s): %w", id, typ.String, err)
		}
		if catIDn.Valid && catIDn.Int64 > 0 {
			if gid, ok := catID[catIDn.Int64]; ok {
				if _, err := m.svc.AddEdge(newID, gid, m.opts.CategoryField, 0); err != nil {
					return nil, fmt.Errorf("migrate: node category %d: %w", id, err)
				}
				st.Edges++
			}
		}
		st.Nodes++
	}
	return st, nil
}

func (m *migrator) filterFields(typ string, ff core.Fields) core.Fields {
	td, ok := m.ts.Type(typ)
	if !ok {
		return ff
	}
	allowed := map[string]bool{}
	for _, f := range td.Fields {
		allowed[f.Name] = true
	}
	out := core.Fields{}
	for k, v := range ff {
		if allowed[k] {
			out[k] = v
		}
	}
	return out
}

func (m *migrator) hasField(typ, name string) bool {
	td, ok := m.ts.Type(typ)
	if !ok {
		return false
	}
	for _, f := range td.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}

func parseFields(s string) core.Fields {
	var m map[string]any
	if s == "" || s == "{}" {
		return core.Fields{}
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return core.Fields{}
	}
	return core.Fields(m)
}
