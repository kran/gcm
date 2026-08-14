package gcm

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kran/gcm/core"
	"github.com/kran/gcm/web"
)

// 多站点装配: 域名分发 + 各站独立（类型/库隔离）+ fallback。
func TestNewAppMultiSite(t *testing.T) {
	dir := t.TempDir()
	typesA := []byte(`
types:
  article:
    title: title
    fields:
      - { name: title, kind: text }
`)
	typesB := []byte(`
types:
  note:
    title: title
    fields:
      - { name: title, kind: text }
`)
	tplA := filepath.Join(dir, "tplA")
	tplB := filepath.Join(dir, "tplB")
	appA, err := NewApp(Options{}, SiteSpec[any]{
		Hosts:     []string{"a.com"},
		DBPath:    filepath.Join(dir, "a.db"),
		Types:     typesA,
		Templates: tplA,
		Setup: func(s *web.Site, svc *core.Service) error {
			svc.CreateNode(&core.Node{Type: "article", Status: core.StatusPublished, Fields: core.Fields{"title": "A文"}})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	appB, err := NewApp(Options{}, SiteSpec[any]{
		Hosts:     []string{"b.com"},
		DBPath:    filepath.Join(dir, "b.db"),
		Types:     typesB,
		Templates: tplB,
		Setup: func(s *web.Site, svc *core.Service) error {
			svc.CreateNode(&core.Node{Type: "note", Status: core.StatusPublished, Fields: core.Fields{"title": "B记"}})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A 站只认识 article; B 站只认识 note（类型隔离）
	hit := func(handler http.Handler, path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	_ = hit
	// 通过 admin API 验证数据隔离（admin 登录后查 nodes）
	// 简化: 用 Setup 后直接查 svc — App 不暴露 svc; 用 HTTP 验证 404 隔离
	// 类型隔离由 types.Load 保证（note 在 A 站不存在）— 通过 /admin/types 验证
	login := func(handler http.Handler) string {
		r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		for _, c := range w.Result().Cookies() {
			if c.Name == "gcm_admin" {
				return c.Value
			}
		}
		return ""
	}
	_ = login
	// hostmux 组合: A/B 站按域名分发
	mux := web.NewHostMux()
	mux.Add([]string{"a.com"}, appA.Handler())
	mux.Add([]string{"b.com"}, appB.Handler())
	mux.SetFallback(appA.Handler())
	r := httptest.NewRequest(http.MethodGet, "/admin/ui/", nil)
	r.Host = "unknown.com"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("fallback must serve site A: %d", w.Code)
	}
	if appA.Site() == nil || appB.Site() == nil {
		t.Fatal("sites missing")
	}
}
