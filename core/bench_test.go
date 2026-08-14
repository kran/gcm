package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

// ── Benchmark: SQLite 在 gcm 设计下的承载 ───────────
//
// 数据规模: 10w nodes（9w article + 5k person + 5k category 树）
//         + 50w edges（每 article 5 authors + 1 category 引用）
// 场景: 单点/列表/出边/反向/子树/组合 — 对齐真实页面查询。
//
// 跑法: go test -bench=. -benchtime=2x ./core/

const benchTypes = `
types:
  category:
    fields:
      - { name: name, kind: textarea }
      - { name: parent, kind: ref, to: category, transitive: true }
  article:
    fields:
      - { name: title, kind: textarea, required: true }
      - { name: body, kind: richtext }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
  person:
    fields:
      - { name: name, kind: textarea, required: true }
`

// benchSeed 建 10w nodes + 50w edges（SQL 批量直插, 绕过 API 校验,
// 模拟已存在的数据; 一次初始化供所有 bench 复用）。
func benchSeed(b *testing.B) *Service {
	db, err := dba.Open("sqlite", filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	if err := migrations.Up(db); err != nil {
		b.Fatal(err)
	}
	ts := types.New()
	if err := ts.Load([]byte(benchTypes)); err != nil {
		b.Fatal(err)
	}
	svc := New(db, ts)

	const (
		nArticle  = 90000
		nPerson   = 5000
		nCategory = 5000
	)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	now := "'2026-01-01 00:00:00'"
	// 1. categories（树: 每 50 个一组, parent = 组内前一个）
	for i := 0; i < nCategory; i++ {
		parent := "0"
		if i > 0 {
			parent = fmt.Sprintf("%d", i)
		}
		sql := fmt.Sprintf(
			`INSERT INTO nodes (type, slug, status, sort, fields, created_at, updated_at)
			 VALUES ('category', 'cat-%d', 1, 0, '{"name":"分类%d"}', %s, %s)`, i, i, now, now)
		// parent 引用单独插入（ref 落 edges）
		if _, err := tx.Add(sql).Exec(); err != nil {
			b.Fatal(err)
		}
		id := int64(i + 1)
		if parent != "0" {
			if _, err := tx.Add(
				`INSERT INTO edges (from_node, field, to_node, sort, created_at)
				 VALUES (#{1}, 'parent', #{2}, 0, datetime('now'))`, id, parent).Exec(); err != nil {
				b.Fatal(err)
			}
		}
	}
	// 2. person
	for i := 0; i < nPerson; i++ {
		sql := fmt.Sprintf(
			`INSERT INTO nodes (type, slug, status, sort, fields, created_at, updated_at)
			 VALUES ('person', 'person-%d', 1, 0, '{"name":"专家%d"}', %s, %s)`, i, i, now, now)
		if _, err := tx.Add(sql).Exec(); err != nil {
			b.Fatal(err)
		}
	}
	// 3. article（body ~1KB 模拟真实大小）+ edges（5 authors + 1 category）
	body := strings.Repeat("产业升级与区域协调发展的路径选择需要系统性的政策工具与市场机制协同。", 20) // ~1KB
	baseID := nCategory + nPerson
	for i := 0; i < nArticle; i++ {
		id := int64(baseID + i + 1)
		sql := fmt.Sprintf(
			`INSERT INTO nodes (type, slug, status, sort, fields, created_at, updated_at)
			 VALUES ('article', 'article-%d', 1, %d, '{"title":"文章标题%d","body":"%s"}', %s, %s)`,
			i, i%100, i, body, now, now)
		if _, err := tx.Add(sql).Exec(); err != nil {
			b.Fatal(err)
		}
		// 5 authors（随机 person）+ 1 category
		for j := 0; j < 5; j++ {
			pid := int64((i*7+j)%nPerson) + int64(nCategory) + 1
			if _, err := tx.Add(
				`INSERT INTO edges (from_node, field, to_node, sort, created_at)
				 VALUES (#{1}, 'authors', #{2}, #{3}, datetime('now'))`, id, pid, j).Exec(); err != nil {
				b.Fatal(err)
			}
		}
		cid := int64(i%nCategory) + 1
		if _, err := tx.Add(
			`INSERT INTO edges (from_node, field, to_node, sort, created_at)
			 VALUES (#{1}, 'categories', #{2}, 0, datetime('now'))`, id, cid).Exec(); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	// ANALYZE: 统计缺失会让 SQLite 选错执行计划（edges 全扫 vs 索引直查）
	if _, err := db.Add("ANALYZE").Exec(); err != nil {
		b.Fatal(err)
	}
	return svc
}

// benchSvc 带 seed 的服务（seed 不计入计时; 数据准备成本单独报告）。
func benchSvc(b *testing.B) *Service {
	b.StopTimer()
	svc := benchSeed(b)
	b.ReportMetric(0, "seed-excluded")
	b.StartTimer()
	return svc
}

// ── 场景 ──────────────────────────────────────────

// BenchmarkGet 单点查询（详情页）。
func BenchmarkGet(b *testing.B) {
	svc := benchSvc(b)
	ids := []int64{1, 50000, 99999}
	for i := 0; i < b.N; i++ {
		if _, err := svc.GetNodeById(ids[i%len(ids)]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkList 列表页（type + status 过滤 + 分页）。
func BenchmarkList(b *testing.B) {
	svc := benchSvc(b)
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.Q(ListQuery{Filter: `(and (= type "article") (= status 1))`, Page: 1, Size: 20}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOutRefs 出边（文章页作者/分类）。
func BenchmarkOutRefs(b *testing.B) {
	svc := benchSvc(b)
	aid := int64(90000 + 5000 + 1000)
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.OutEdges("article", aid, "authors", 1, 20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInRefs 反向查询（专家页"他的文章"）。
func BenchmarkInRefs(b *testing.B) {
	svc := benchSvc(b)
	pid := int64(5000 + 100)
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.InEdges(pid, "authors", 1, 20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSubtree 分类子树（分类页范围）。
func BenchmarkSubtree(b *testing.B) {
	svc := benchSvc(b)
	for i := 0; i < b.N; i++ {
		if _, err := svc.Subtree("category", 1, "parent", 20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListWithRef 组合: 分类子树下的内容（模拟分类页核心查询,
// 一次 JOIN edges + 子树 IN, 而非逐分类子查询）。
func BenchmarkListWithRef(b *testing.B) {
	svc := benchSvc(b)
	// 子树 id 集合 + 一次 JOIN 内容列表
	for i := 0; i < b.N; i++ {
		ids, err := svc.Subtree("category", 1, "parent", 20)
		if err != nil {
			b.Fatal(err)
		}
		// 子树 id → 占位符列表
		ph := make([]string, len(ids))
		args := []any{"article", StatusPublished}
		for j, id := range ids {
			ph[j] = fmt.Sprintf("#{%d}", len(args)+1)
			args = append(args, id)
		}
		var rows []Node
		if err := svc.DB().Add(
			`SELECT n.* FROM nodes n
			 WHERE n.type = #{1} AND n.status = #{2}
			   AND n.id IN (SELECT from_node FROM edges
			                WHERE field = 'categories' AND to_node IN (`+strings.Join(ph, ",")+`))
			 ORDER BY n.sort DESC, n.id DESC LIMIT 20`,
			args...).List(&rows); err != nil {
			b.Fatal(err)
		}
	}
}
