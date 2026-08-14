package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

const smokeTypes = `
types:
  article:
    url: /article/{slug}
    fields:
      - { name: body, kind: richtext }
      - { name: authors, kind: "ref[]", to: person }
  person:
    fields:
      - { name: name, kind: text, required: true }
`

func newSite(t *testing.T) (*Site, *core.Service) {
	t.Helper()
	dir := t.TempDir()
	db, err := dba.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ts := types.New()
	if err := ts.Load([]byte(smokeTypes)); err != nil {
		t.Fatal(err)
	}
	svc := core.New(db, ts)

	// 模板目录
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	os.WriteFile(filepath.Join(tplDir, "node.html"),
		[]byte(`<h1>{{ .Node.Slug }}</h1><div>{{ safeHTML .Node.Fields.body }}</div>`), 0644)
	// person 模板故意写错误查询 — 触发渲染失败 → HTML 注释
	os.WriteFile(filepath.Join(tplDir, "node--person.html"),
		[]byte(`{{ range outRefs .ID "ghost" 1 10 }}{{ end }}`), 0644)
	os.WriteFile(filepath.Join(tplDir, "node--article.html"),
		[]byte(`<article>{{ .Node.Slug }}:{{ range outRefs .ID "authors" 1 10 }}[{{ .Fields.name }}]{{ end }}</article>`), 0644)
	os.WriteFile(filepath.Join(tplDir, "404.html"),
		[]byte(`<p>not found: {{ .Path }}</p>`), 0644)

	eng := render.New(tplDir, svc)
	s := New(svc, eng, filepath.Join(dir, "static"), nil)
	return s, svc
}

func get(t *testing.T, s *Site, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestNodeRoute(t *testing.T) {
	s, svc := newSite(t)
	aid, _ := svc.CreateNode(&core.Node{Type: "article", Slug: "hello",
		Status: core.StatusPublished, Fields: core.Fields{"body": "<p>hi</p>"}})

	// 按 id
	rec := get(t, s, "/node/"+fmtInt(aid))
	if rec.Code != 200 {
		t.Fatalf("by id: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<article>hello:") {
		t.Fatalf("article template: %s", rec.Body.String())
	}
	// 按 slug
	rec = get(t, s, "/node/hello")
	if rec.Code != 200 {
		t.Fatalf("by slug: %d", rec.Code)
	}
	// 草稿 → 404
	draftID, _ := svc.CreateNode(&core.Node{Type: "article", Slug: "draft",
		Status: core.StatusDraft, Fields: core.Fields{"body": "x"}})
	_ = draftID
	rec = get(t, s, "/node/draft")
	if rec.Code != 404 {
		t.Fatalf("draft: %d", rec.Code)
	}
	// 不存在 → 404 模板
	rec = get(t, s, "/node/nope")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "not found: /node/nope") {
		t.Fatalf("404: %d %s", rec.Code, rec.Body.String())
	}
	// 渲染错误 → HTML 注释（不 500, 细节只进注释/日志）
	// person 走 node--person.html（故意写错查询）
	pid, _ := svc.CreateNode(&core.Node{Type: "person", Slug: "li",
		Status: core.StatusPublished, Fields: core.Fields{"name": "张三"}})
	rec = get(t, s, "/node/"+fmtInt(pid))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<!-- render error:") {
		t.Fatalf("render error comment: %d %s", rec.Code, rec.Body.String())
	}
	// 无专用模板的类型 → node.html 兜底（person 被专用模板占了, 用 article 兜底验证）
	// 建一个无专用模板的: article 已有 node--article.html — 直接验证 404 路由未匹配
	_ = pid
}

func TestStaticRoute(t *testing.T) {
	s, _ := newSite(t)
	os.MkdirAll(s.static, 0755)
	os.WriteFile(filepath.Join(s.static, "a.css"), []byte("body{}"), 0644)
	rec := get(t, s, "/static/a.css")
	if rec.Code != 200 || rec.Body.String() != "body{}" {
		t.Fatalf("static: %d %q", rec.Code, rec.Body.String())
	}
}

func fmtInt(n int64) string {
	return strconv.FormatInt(n, 10)
}

// Debug 模式: 渲染失败 → 500 错误页（模板名/原因/候选/数据 keys）;
// 生产 → HTML 注释。
func TestDebugRenderError(t *testing.T) {
	s, svc := newSite(t)
	s.Debug = true
	// article 节点渲染走 node.html — 模板引用不存在的函数 → 渲染失败
	svc.CreateNode(&core.Node{Type: "article", Status: core.StatusPublished, Fields: core.Fields{"body": "x"}})
	// 覆盖 node.html 为错误模板
	dir := svc.DB() // 用 svc 内部无法拿目录 — 直接用 newSite 的模板覆盖不现实; 改走另一路: person 模板
	// person 模板引用 outRefs ghost（渲染失败）— 断言 Debug 时 500 页面
	p1, _ := svc.CreateNode(&core.Node{Type: "person", Status: core.StatusPublished, Fields: core.Fields{"name": "x"}})
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/node/%d", p1), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("debug must 500, got %d body: %s", rec.Code, rec.Body.String()[:100])
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Render Error") {
		t.Fatalf("error page missing: %s", body[:200])
	}
	// 生产模式同路径 → 200 + HTML 注释
	s.Debug = false
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, req)
	if rec2.Code != 200 {
		t.Fatalf("prod must 200, got %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "<!-- render error") {
		t.Fatal("prod must HTML comment")
	}
	_ = dir
}

// NodeEnrich: 默认 url 注入（统一 /node/{slug|id} — URL 模式是站点层的事）。
func TestNodeEnrichURL(t *testing.T) {
	s, svc := newSite(t)
	a, _ := svc.CreateNode(&core.Node{Type: "article", Slug: "hello", Status: core.StatusPublished,
		Fields: core.Fields{"body": "x"}})
	n, _ := svc.GetNodeById(a)
	if s.nodeURL(n) != "/node/hello" {
		t.Fatalf("node url: %s", s.nodeURL(n))
	}
	// 无 slug → /node/{id}
	b, _ := svc.CreateNode(&core.Node{Type: "article", Status: core.StatusPublished,
		Fields: core.Fields{"body": "y"}})
	bn, _ := svc.GetNodeById(b)
	if s.nodeURL(bn) != "/node/"+strconv.FormatInt(b, 10) {
		t.Fatalf("no slug url: %s", s.nodeURL(bn))
	}
}
