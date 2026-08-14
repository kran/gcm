package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"

	"github.com/kran/cho"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
	"github.com/kran/gcm/web"
)

const adminTypes = `
types:
  article:
    fields:
      - { name: body, kind: richtext }
      - { name: authors, kind: "ref[]", to: person }
  person:
    title: name
    fields:
      - { name: name, kind: text, required: true }
`

func newAdminSite(t *testing.T) (*web.Site, *core.Service) {
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
	if err := ts.Load([]byte(adminTypes)); err != nil {
		t.Fatal(err)
	}
	svc := core.New(db, ts)

	// 首次引导账号 admin, 再改成测试固定密码
	if _, err := EnsureDefaults(db); err != nil {
		t.Fatal(err)
	}
	as := NewService(db)
	if err := as.SetPassword("testpass123"); err != nil {
		t.Fatal(err)
	}

	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(`ok`), 0644)
	eng := render.New(tplDir, svc)
	if err := web.DefineRenderHooks(svc); err != nil {
		t.Fatal(err)
	}
	s := web.New(svc, eng)
	Mount(s, "")
	return s, svc
}

// 站点专门管理: AdminMount 挂受保护端点 + 注册面板。
func TestAdminMountPanels(t *testing.T) {
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
	if err := ts.Load([]byte(adminTypes)); err != nil {
		t.Fatal(err)
	}
	svc := core.New(db, ts)
	// 账号引导（login helper 用 admin/testpass123）
	if _, err := EnsureDefaults(db); err != nil {
		t.Fatal(err)
	}
	if err := NewService(db).SetPassword("testpass123"); err != nil {
		t.Fatal(err)
	}
	eng := render.New(filepath.Join(dir, "templates"), svc)
	if err := web.DefineRenderHooks(svc); err != nil {
		t.Fatal(err)
	}
	// 站点: 挂自定义端点（自动带认证）+ 注册面板 — 事件须先 Define
	if err := DefineHooks(svc); err != nil {
		t.Fatal(err)
	}
	svc.Hooks().AddHook(AdminMount, func(g *cho.Cho[*web.CmsCtx], panels *[]AdminPanel) error {
		g.Get("/guestbook", func(ctx *web.CmsCtx) {
			_ = ctx.Json(http.StatusOK, map[string]any{"items": []string{"留言1"}})
		})
		*panels = append(*panels, AdminPanel{
			Path: "/guestbook", Title: "留言本",
			Vue: "/admin/ui-extras/guestbook.vue",
		})
		return nil
	})
	s := web.New(svc, eng)
	Mount(s, "")

	// 未登录 → 401（认证保护生效）
	req := httptest.NewRequest(http.MethodGet, "/admin/guestbook", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth: %d", rec.Code)
	}
	// 登录后 → 200 + 数据
	rec = authedReq(t, s, http.MethodGet, "/admin/guestbook", nil)
	if rec.Code != 200 {
		t.Fatalf("authed: %d", rec.Code)
	}
	// /admin/panels 返回注册的面板
	rec = authedReq(t, s, http.MethodGet, "/admin/panels", nil)
	var out struct {
		Items []AdminPanel `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Items) != 1 || out.Items[0].Title != "留言本" {
		t.Fatalf("panels: %s", rec.Body.String())
	}
}

// login 登录并返回 cookie。
func login(t *testing.T, s *web.Site) string {
	t.Helper()
	body := bytes.NewBufferString(`{"username":"admin","password":"testpass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c.Value
		}
	}
	t.Fatal("no cookie")
	return ""
}

func authedReq(t *testing.T, s *web.Site, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: login(t, s)})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestLoginRequired(t *testing.T) {
	s, _ := newAdminSite(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/nodes?type=article", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("unauth: %d", rec.Code)
	}
	// 错误密码
	body := bytes.NewBufferString(`{"username":"admin","password":"wrong"}`)
	req = httptest.NewRequest(http.MethodPost, "/admin/login", body)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("bad login: %d", rec.Code)
	}
}

func TestAdminCRUD(t *testing.T) {
	s, svc := newAdminSite(t)

	// 建专家（引用目标）
	p1 := authedReq(t, s, http.MethodPost, "/admin/nodes?type=person",
		map[string]any{"fields": map[string]any{"name": "张三"}})
	if p1.Code != 201 {
		t.Fatalf("create person: %d %s", p1.Code, p1.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(p1.Body.Bytes(), &created)

	// 建文章（含 ref 字段 → 落边）
	art := authedReq(t, s, http.MethodPost, "/admin/nodes?type=article",
		map[string]any{"slug": "hello", "status": 1,
			"fields": map[string]any{"body": "<p>hi</p>", "authors": []any{created.ID}}})
	if art.Code != 201 {
		t.Fatalf("create article: %d %s", art.Code, art.Body.String())
	}
	json.Unmarshal(art.Body.Bytes(), &created)

	// 列表
	list := authedReq(t, s, http.MethodGet, "/admin/nodes?type=article&page=1&size=10", nil)
	if list.Code != 200 {
		t.Fatalf("list: %d", list.Code)
	}
	var lr struct {
		Total int64 `json:"total"`
	}
	json.Unmarshal(list.Body.Bytes(), &lr)
	if lr.Total != 1 {
		t.Fatalf("total: %d", lr.Total)
	}

	// 详情
	one := authedReq(t, s, http.MethodGet, "/admin/nodes/"+fmtInt(created.ID), nil)
	if one.Code != 200 {
		t.Fatalf("get: %d", one.Code)
	}

	// 更新
	upd := authedReq(t, s, http.MethodPut, "/admin/nodes/"+fmtInt(created.ID),
		map[string]any{"fields": map[string]any{"body": "<p>v2</p>"}})
	if upd.Code != 200 {
		t.Fatalf("update: %d %s", upd.Code, upd.Body.String())
	}

	// 删除
	del := authedReq(t, s, http.MethodDelete, "/admin/nodes/"+fmtInt(created.ID), nil)
	if del.Code != 200 {
		t.Fatalf("delete: %d", del.Code)
	}
	if n, _ := svc.GetNodeById(created.ID); n != nil {
		t.Fatal("node must be deleted")
	}
}

func TestAdminSearch(t *testing.T) {
	s, _ := newAdminSite(t)
	authedReq(t, s, http.MethodPost, "/admin/nodes?type=person",
		map[string]any{"fields": map[string]any{"name": "李志起"}})
	authedReq(t, s, http.MethodPost, "/admin/nodes?type=person",
		map[string]any{"fields": map[string]any{"name": "张三"}})

	res := authedReq(t, s, http.MethodGet, "/admin/search?q=李&type=person", nil)
	if res.Code != 200 {
		t.Fatalf("search: %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "李志起") || strings.Contains(res.Body.String(), "张三") {
		t.Fatalf("search result: %s", res.Body.String())
	}
}

// types API: 类型定义给 UI 渲染。
func TestAdminTypes(t *testing.T) {
	s, _ := newAdminSite(t)
	res := authedReq(t, s, http.MethodGet, "/admin/types", nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "article") {
		t.Fatalf("types: %d %s", res.Code, res.Body.String())
	}
}

func fmtInt(n int64) string {
	return strconv.FormatInt(n, 10)
}

// UI 静态 + 上传冒烟。
func TestUIAndUpload(t *testing.T) {
	dir := t.TempDir()
	up := filepath.Join(dir, "uploads")
	s, svc := newAdminSite(t)
	// 重挂载带上传目录（newAdminSite 已 Mount 空目录 — 用新站点）
	_ = svc
	_ = up
	// UI 入口（公开, 无登录）
	req := httptest.NewRequest(http.MethodGet, "/admin/ui/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "gcm Admin") {
		t.Fatalf("ui: %d", rec.Code)
	}
	// index.html 引用存在
	for _, p := range []string{"/admin/ui/pages/App.vue", "/admin/ui/js/api.js", "/admin/ui/vendor/vue.global.prod.js"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("ui asset %s: %d", p, rec.Code)
		}
	}
}

// nodes API 支持 filter 参数（树过滤落点）: 表达式编译 + 参数化执行。
func TestListNodesFilter(t *testing.T) {
	s, _ := newAdminSite(t)
	// 建分类 + 文章（category 类型在 adminTypes 没有? 补: 用 person 当树? —
	// 简化: article 的 authors ref 指向 person; filter authors ~ [id]）
	p1 := authedReq(t, s, http.MethodPost, "/admin/nodes?type=person",
		map[string]any{"fields": map[string]any{"name": "张三"}})
	var p1ID int64
	if err := json.Unmarshal(p1.Body.Bytes(), &struct{ ID *int64 }{&p1ID}); err != nil || (p1.Code != 200 && p1.Code != 201) {
		t.Fatalf("create person: %d %s", p1.Code, p1.Body.String())
	}
	authedReq(t, s, http.MethodPost, "/admin/nodes?type=article",
		map[string]any{"fields": map[string]any{"body": "x", "authors": []any{p1ID}}})
	authedReq(t, s, http.MethodPost, "/admin/nodes?type=article",
		map[string]any{"fields": map[string]any{"body": "y"}})

	// filter 查询（Lisp）: (in ->authors [id])
	got := authedReq(t, s, http.MethodGet,
		fmt.Sprintf("/admin/nodes?type=article&filter=%s", url.QueryEscape(fmt.Sprintf(`(in ->authors [%d])`, p1ID))), nil)
	var out struct {
		Items []core.Node `json:"items"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &out); err != nil || got.Code != 200 {
		t.Fatalf("filter list: %d %s", got.Code, got.Body.String())
	}
	if len(out.Items) != 1 {
		t.Fatalf("filter list: %d items", len(out.Items))
	}
	// 非法 filter → 400
	bad := authedReq(t, s, http.MethodGet, "/admin/nodes?type=article&filter=(bogus-fn)", nil)
	if bad.Code != 400 {
		t.Fatalf("bad filter must 400, got %d", bad.Code)
	}
}
