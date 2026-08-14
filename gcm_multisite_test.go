package gcm

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kran/gcm/core"
	"github.com/kran/gcm/web"
)

// 多站组合: 两个独立站点（各自类型/模板/库）+ HostMux 按域名分发。
func TestMultiSite(t *testing.T) {
	dir := t.TempDir()

	typesA := []byte("types:\n  article:\n    fields:\n      - { name: body, kind: richtext }\n")
	typesB := []byte("types:\n  note:\n    fields:\n      - { name: text, kind: text }\n")

	mkTpl := func(name, content string) string {
		td := filepath.Join(dir, name)
		os.MkdirAll(td, 0755)
		os.WriteFile(filepath.Join(td, "node.html"), []byte(content), 0644)
		return td
	}

	appA, err := NewSite(SiteSpec{
		Hosts:     []string{"site-a.com"},
		DBPath:    filepath.Join(dir, "a.db"),
		Types:     typesA,
		Templates: mkTpl("a", `<h1>A:{{ .Node.Slug }}</h1>`),
		Setup: func(s *web.Site, svc *core.Service) error {
			svc.CreateNode(&core.Node{Type: "article", Slug: "hello", Status: core.StatusPublished, Fields: core.Fields{"body": "x"}})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	appB, err := NewSite(SiteSpec{
		Hosts:     []string{"site-b.com"},
		DBPath:    filepath.Join(dir, "b.db"),
		Types:     typesB,
		Templates: mkTpl("b", `<h1>B:{{ .Node.Slug }}</h1>`),
		Setup: func(s *web.Site, svc *core.Service) error {
			svc.CreateNode(&core.Node{Type: "note", Slug: "hi", Status: core.StatusPublished, Fields: core.Fields{"text": "x"}})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := web.NewHostMux()
	mux.Add([]string{"site-a.com"}, appA.Handler())
	mux.Add([]string{"site-b.com"}, appB.Handler())
	mux.SetFallback(appA.Handler())

	hit := func(host, path string) string {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Body.String()
	}
	if got := hit("site-a.com", "/node/hello"); !strings.Contains(got, "A:hello") {
		t.Fatalf("site-a: %s", got)
	}
	if got := hit("site-b.com", "/node/hi"); !strings.Contains(got, "B:hi") {
		t.Fatalf("site-b: %s", got)
	}
	// 类型隔离: B 站无 article 类型
	if got := hit("site-b.com", "/node/hello"); strings.Contains(got, "hello") {
		t.Fatalf("site-b leak: %s", got)
	}
	// fallback: 未知 host → A
	if got := hit("unknown.com", "/node/hello"); !strings.Contains(got, "A:hello") {
		t.Fatalf("fallback: %s", got)
	}
}
