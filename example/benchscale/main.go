// benchscale 数据量边界探索: 不同规模下各查询延迟, 找 200ms 边界。
//
//	跑法: go run ./example/benchscale
//
// 规模阶梯: 10w/25w/50w/100w/200w nodes × 5 引用 edges。
// 每个规模: seed（多值批量插入）→ 测 6 场景（各 5 次取中位）→ 记录文件大小。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

const typesYAML = `
types:
  category:
    fields:
      - { name: name, kind: string }
      - { name: parent, kind: ref, to: category, transitive: true }
  article:
    fields:
      - { name: title, kind: string, required: true }
      - { name: body, kind: richtext }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
  person:
    fields:
      - { name: name, kind: string, required: true }
`

type scale struct {
	name  string
	nodes int
	edges int
}

var scales = []scale{
	{"10w", 100_000, 500_000},
	{"25w", 250_000, 1_250_000},
	{"50w", 500_000, 2_500_000},
	{"100w", 1_000_000, 5_000_000},
	{"200w", 2_000_000, 10_000_000},
}

func main() {
	fmt.Printf("%-8s %-10s %-9s %-9s %-9s %-9s %-9s %-9s\n",
		"规模", "文件", "GetNodeById", "List", "OutEdges", "InEdges", "Subtree", "组合")
	for _, sc := range scales {
		runScale(sc)
	}
}

func runScale(sc scale) {
	dir, _ := os.MkdirTemp("", "gcm-scale")
	defer os.RemoveAll(dir)
	db, err := dba.Open("sqlite", filepath.Join(dir, "scale.db"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := migrations.Up(db); err != nil {
		panic(err)
	}
	ts := types.New()
	if err := ts.Load([]byte(typesYAML)); err != nil {
		panic(err)
	}
	svc := core.New(db, ts)

	t0 := time.Now()
	seed(db, sc)
	size := fileSize(filepath.Join(dir, "scale.db"))
	fmt.Printf("%-8s %-10s ", sc.name, humanSize(size))
	_ = t0

	// 基准节点
	articleID := int64(sc.nodes*4/5 + 100)
	personID := int64(sc.nodes/10 + 50)

	bench("GetNodeById", func() { svc.GetNodeById(articleID) })
	bench("QueryPage", func() {
		svc.QueryPage(core.ListQuery{Filter: `(and (= type "article") (= status 1))`, Page: 1, Size: 20})
	})
	bench("OutEdges", func() { svc.OutEdges("article", articleID, "authors", 1, 20) })
	bench("InEdges", func() { svc.InEdges(personID, "authors", 1, 20) })
	bench("Subtree", func() { svc.Subtree("category", 1, "parent", 20) })
	bench("Combo", func() { combo(svc) })
	fmt.Println()
}

// bench 跑 5 次取中位数并打印。
func bench(name string, fn func()) {
	times := make([]time.Duration, 0, 5)
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		fn()
		times = append(times, time.Since(t0))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	fmt.Printf("%-9s ", times[len(times)/2].Round(time.Microsecond*100))
}

// combo 组合: 分类子树下的内容（真实分类页写法）。
func combo(svc *core.Service) {
	ids, err := svc.Subtree("category", 1, "parent", 20)
	if err != nil {
		return
	}
	ph := make([]string, len(ids))
	args := []any{"article", core.StatusPublished}
	for j, id := range ids {
		ph[j] = fmt.Sprintf("#{%d}", len(args)+1)
		args = append(args, id)
	}
	var rows []core.Node
	_ = svc.DB().Add(
		`SELECT n.* FROM nodes n
		 WHERE n.type = #{1} AND n.status = #{2}
		   AND n.id IN (SELECT from_node FROM edges
		                WHERE field = 'categories' AND to_node IN (`+strings.Join(ph, ",")+`))
		 ORDER BY n.sort DESC, n.id DESC LIMIT 20`,
		args...).List(&rows)
}

// seed 多值批量插入（1000w 行分钟级完成）。
func seed(db *dba.SQL, sc scale) {
	tx, err := db.Begin()
	if err != nil {
		panic(err)
	}
	nCat := sc.nodes / 20
	nPerson := sc.nodes / 20
	nArticle := sc.nodes - nCat - nPerson

	// categories（线性树 parent）
	batchInsert(tx, "nodes", nCat, func(i int) string {
		return fmt.Sprintf("('category','cat-%d',1,0,'{\"name\":\"分类%d\"}',datetime('now'),datetime('now'))", i, i)
	})
	for i := 1; i < nCat; i++ {
		batchInsert(tx, "edges", 1, func(j int) string {
			return fmt.Sprintf("(%d,'parent',%d,0,datetime('now'))", i+1, i)
		})
	}
	// person
	batchInsert(tx, "nodes", nPerson, func(i int) string {
		return fmt.Sprintf("('person','person-%d',1,0,'{\"name\":\"专家%d\"}',datetime('now'),datetime('now'))", i, i)
	})
	// article（body 200B 模拟）+ edges（5 authors + 1 category）
	body := strings.Repeat("产业升级与区域协调发展的路径选择需要系统性的政策工具与市场机制协同。", 4)
	base := nCat + nPerson
	batchInsert(tx, "nodes", nArticle, func(i int) string {
		return fmt.Sprintf("('article','article-%d',1,%d,'{\"title\":\"文章标题%d\",\"body\":\"%s\"}',datetime('now'),datetime('now'))",
			i, i%100, i, body)
	})
	// edges: authors ×5 + categories ×1
	batchInsert(tx, "edges", nArticle*5, func(i int) string {
		articleIdx := i / 5
		j := i % 5
		from := int64(base + articleIdx + 1)
		pid := int64((articleIdx*7+j)%nPerson) + int64(nCat) + 1
		return fmt.Sprintf("(%d,'authors',%d,%d,datetime('now'))", from, pid, j)
	})
	batchInsert(tx, "edges", nArticle, func(i int) string {
		from := int64(base + i + 1)
		cid := int64(i%nCat) + 1
		return fmt.Sprintf("(%d,'categories',%d,0,datetime('now'))", from, cid)
	})
	if err := tx.Commit(); err != nil {
		panic(err)
	}
}

// batchInsert 多值批量插入（每批 200 行）。
func batchInsert(tx *dba.SQL, table string, n int, row func(i int) string) {
	const batch = 200
	cols := "(type, slug, status, sort, fields, created_at, updated_at)"
	if table == "edges" {
		cols = "(from_node, field, to_node, sort, created_at)"
	}
	var sb strings.Builder
	for start := 0; start < n; start += batch {
		end := start + batch
		if end > n {
			end = n
		}
		sb.Reset()
		sb.WriteString("INSERT INTO " + table + " " + cols + " VALUES ")
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString(row(i))
		}
		if _, err := tx.Add(sb.String()).Exec(); err != nil {
			panic(err)
		}
	}
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func humanSize(n int64) string {
	switch {
	case n > 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n > 1<<20:
		return fmt.Sprintf("%.0fMB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	}
}
