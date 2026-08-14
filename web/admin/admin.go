package admin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kran/cho"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/hook"
	"github.com/kran/gcm/types"
	"github.com/kran/gcm/web"
)

// backend 管理 handler 组: 账号服务 + 核心引擎 + 类型系统。
type backend struct {
	svc       *Service
	core      *core.Service
	ts        *types.Types
	uploadDir string
	panels    []AdminPanel // 站点面板（AdminMount 收集）
}

// internal 服务器错误统一出口: 细节进日志, 响应透传（admin 是站长工具,
// fail-loud 可见性优先; 敏感 SQL 细节留在日志）。
func (b *backend) internal(ctx *web.CmsCtx, err error) {
	slog.Error("admin internal", "path", ctx.R.URL.Path, "err", err)
	_ = ctx.Error(http.StatusInternalServerError, "internal error")
}

// bad 客户端错误（校验/参数）: 直接透传, 前端表单回显。
func (b *backend) bad(ctx *web.CmsCtx, err error) {
	ctx.Error(http.StatusBadRequest, err.Error())
}

// ── UI 静态 + 上传 ────────────────────────────────

// uiFile 管理 UI 静态资源（无构建 Vue, embed 随库分发）。
// 公开访问: SPA 自行处理登录态; API 部分仍受 authed 保护。
// 无构建 = UI 随库版本变化频繁 — 禁缓存防浏览器吃旧壳。
func (b *backend) uiFile(ctx *web.CmsCtx) {
	ctx.SetHeader("Cache-Control", "no-cache")
	p := path.Clean(strings.TrimPrefix(ctx.R.URL.Path, "/admin/ui/"))
	if p == "." || p == "/" || p == "" {
		p = "index.html"
	}
	data, err := fs.ReadFile(uiFS, p)
	if err != nil {
		ctx.String(http.StatusNotFound, "404 not found")
		return
	}
	ctx.SetHeader("Content-Type", mime.TypeByExtension(filepath.Ext(p)))
	_, _ = ctx.W.Write(data)
}

// uploadAllowExt 上传扩展名白名单。svg/html 刻意排除（可执行内容,
// 同源服务 = 存储型 XSS）。
var uploadAllowExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".ico": true,
	".pdf": true, ".zip": true,
	".mp4": true, ".webm": true, ".mp3": true, ".wav": true,
}

// upload 处理 multipart 上传: 大小上限 → 扩展名白名单 → 随机文件名 → 落 uploads。
func (b *backend) upload(ctx *web.CmsCtx) {
	if b.uploadDir == "" {
		ctx.Error(http.StatusBadRequest, "uploads disabled")
		return
	}
	maxBytes := int64(8 << 20) // 8MB 硬上限
	ctx.R.Body = http.MaxBytesReader(ctx.W, ctx.R.Body, maxBytes)
	if err := ctx.R.ParseMultipartForm(maxBytes); err != nil {
		ctx.Error(http.StatusRequestEntityTooLarge, "file too large (max 8MB)")
		return
	}
	f, fh, err := ctx.R.FormFile("file")
	if err != nil {
		ctx.Error(http.StatusBadRequest, "missing file field 'file'")
		return
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !uploadAllowExt[ext] {
		ctx.Error(http.StatusBadRequest, "file type not allowed: "+ext)
		return
	}
	base := strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename))
	base = strings.ReplaceAll(base, " ", "-")
	rand4 := make([]byte, 4)
	if _, err := rand.Read(rand4); err != nil {
		b.internal(ctx, err)
		return
	}
	name := fmt.Sprintf("%d-%s-%s%s", time.Now().Unix(), hex.EncodeToString(rand4), base, ext)
	if err := os.MkdirAll(b.uploadDir, 0755); err != nil {
		b.internal(ctx, err)
		return
	}
	dst, err := os.Create(filepath.Join(b.uploadDir, name))
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		b.internal(ctx, err)
		return
	}
	dst.Close()
	_ = ctx.Json(http.StatusOK, map[string]any{"name": name, "path": "/uploads/" + name})
}

// AdminPanel 一个后台面板（站点专门管理: 菜单项 + 组件入口）。
type AdminPanel struct {
	Path  string `json:"path"`  // 组内 API 前缀, 如 "/guestbook"
	Title string `json:"title"` // 后台菜单名
	Vue   string `json:"vue"`   // 面板组件完整 URL（认证路由, 站点自己挂）
}

// AdminMount 后台面板挂载事件（admin.Mount 建 /admin 认证组后 Fire）:
// 原型 func(g *cho.Cho[*web.CmsCtx], panels *[]AdminPanel) error —
// 站点 AddHook 挂自己的受保护端点（自动带登录守卫）+ 注册后台菜单。
const AdminMount = "admin.mount"

// Mount 挂载 /admin 组到站点（登录保护; 公开入口: login/ui/upload）。
// uploadDir 是上传落盘目录（可为空 = 禁用上传）; /uploads/* 服务由站点
// 装配层挂载（gcm.NewApp — 前台资源不依赖 admin 是否启用）。
// svc/ts 从 site 上下文取（site.Service() / svc.Types()）— 装配参数最小化。
// DefineHooks 声明后台事件（AdminMount — 装配早期调用, 站点 Setup/AddHook 前）;
// 与 web.DefineRenderHooks 同位置（gcm 装配序列）。
func DefineHooks(svc *core.Service) error {
	return svc.Hooks().Define(hook.Spec{Name: AdminMount,
		Proto: func(*cho.Cho[*web.CmsCtx], *[]AdminPanel) error { return nil }})
}

func Mount(s *web.Site, uploadDir string) {
	svc := s.Service()
	b := &backend{svc: NewService(s.DB()), core: svc, ts: svc.Types(), uploadDir: uploadDir}

	// /admin 组: 公开 login/logout/ui/upload, 其余登录保护
	s.Group("/admin", func(g *cho.Cho[*web.CmsCtx]) {
		g.Post("/login", b.login)
		g.Post("/logout", b.logout)
		g.Get("/ui/*", b.uiFile)
		g.Post("/upload", b.upload)
		g.Group("", func(authed *cho.Cho[*web.CmsCtx]) {
			authed.UseCtx(b.requireAuth)
			// 站点专门管理: Fire AdminMount — 站点挂受保护端点 + 注册面板
			panels := &[]AdminPanel{}
			if err := svc.Hooks().Fire(AdminMount, authed, panels); err != nil {
				panic(fmt.Sprintf("admin: fire mount hook: %v", err))
			}
			b.panels = *panels
			authed.Get("/panels", b.listPanels)
			authed.Get("/me", b.me)
			authed.Get("/types", b.types)
			authed.Get("/nodes", b.listNodes)
			authed.Post("/nodes", b.createNode)
			authed.Get("/nodes/{id}", b.getNode)
			authed.Put("/nodes/{id}", b.updateNode)
			authed.Delete("/nodes/{id}", b.deleteNode)
			authed.Get("/search", b.search)
			authed.Get("/tree", b.tree)
			authed.Get("/expand", b.expand)
			authed.Post("/password", b.changePassword)
			authed.Get("/settings", b.listSettings)
			authed.Post("/settings", b.setSetting)
			authed.Delete("/settings/{key}", b.deleteSetting)
		})
	})
}

// ── 认证 ─────────────────────────────────────────

func (b *backend) login(ctx *web.CmsCtx) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	if !b.svc.VerifyPassword(in.Username, in.Password) {
		ctx.Error(http.StatusUnauthorized, "invalid credentials")
		return
	}
	key, err := b.svc.NewSession()
	if err != nil {
		b.internal(ctx, err)
		return
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: cookieName, Value: key,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(sessionTTL),
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) logout(ctx *web.CmsCtx) {
	http.SetCookie(ctx.W, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// listPanels 站点面板列表（前端动态注册菜单 + 组件）。
func (b *backend) listPanels(ctx *web.CmsCtx) {
	_ = ctx.Json(http.StatusOK, map[string]any{"items": b.panels})
}

// requireAuth 会话校验中间件（cho 类型化中间件: 校验失败短路）。
func (b *backend) requireAuth(ctx *web.CmsCtx, next func()) {
	c, err := ctx.R.Cookie(cookieName)
	if err != nil || !b.svc.ValidSession(c.Value) {
		ctx.Error(http.StatusUnauthorized, "unauthorized")
		return
	}
	next()
}

func (b *backend) me(ctx *web.CmsCtx) {
	_ = ctx.Json(http.StatusOK, map[string]any{"username": "admin"})
}

// ── 类型定义 ─────────────────────────────────────

// types 类型定义（admin UI 动态表单渲染用）。
func (b *backend) types(ctx *web.CmsCtx) {
	// kind 名即控件名（B 方案）: 前端按 kind 直接渲染, 无需 kinds 映射。
	_ = ctx.Json(http.StatusOK, map[string]any{"types": b.ts.Defs()})
}

// ── 节点 CRUD ───────────────────────────────────

// listNodes 按 type 分页列表（管理通道: 含草稿, 全部状态）。
func (b *backend) listNodes(ctx *web.CmsCtx) {
	typ := ctx.Query("type")
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 20)
	if size > 100 {
		size = 100
	}
	if typ == "" {
		ctx.Error(http.StatusBadRequest, "type required")
		return
	}
	// filter 参数: 完整查询能力（树过滤/任意字段筛选）; q 参数: 标题模糊搜索
	// （like 参数化, 与 filter 叠加）。表达式经 filter 引擎编译 + 参数化。
	var list []core.Node
	var total int64
	var err error
	filter := strings.TrimSpace(ctx.Query("filter"))
	q := strings.TrimSpace(ctx.Query("q"))
	// filter = Lisp 表达式; q = 标题模糊（合成 (like title {:q})）
	params := map[string]any{}
	if q != "" {
		params["q"] = "%" + q + "%"
		if filter != "" {
			filter = "(and " + filter + " (like title {:q}))"
		} else {
			filter = "(like title {:q})"
		}
	}
	// 统一 Q: 类型过滤合成（filter 空时仅 (= type "x")）
	f := `(= type "` + typ + `")`
	if filter != "" {
		f = `(and (= type "` + typ + `") ` + filter + `)`
	}
	list, total, err = b.core.Q(core.ListQuery{Filter: f, Page: page, Size: size}, params)
	if err != nil {
		// filter 编译错误（filter-lisp: 前缀）= 客户端参数 → 400; 其余 → 500
		if strings.Contains(err.Error(), "filter-lisp:") {
			b.bad(ctx, err)
		} else {
			b.internal(ctx, err)
		}
		return
	}
	// 列表默认展开全部出边 ref 字段（一层, 批量 — 查询次数=字段数, 与页大小无关）
	expanded, err := b.expandMany(list)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{
		"items": expanded, "total": total, "page": page, "size": size,
	})
}

// expandMany 列表批量展开全部出边 ref 字段 — "*" 引擎语义（core 解析）。
func (b *backend) expandMany(nodes []core.Node) ([]core.Node, error) {
	if len(nodes) == 0 {
		return nodes, nil
	}
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	expanded, err := b.core.ExpandPathMany(ids, "*")
	if err != nil {
		return nil, err
	}
	out := make([]core.Node, 0, len(expanded))
	for _, p := range expanded {
		out = append(out, *p)
	}
	return out, nil
}

// nodeInput 创建/更新输入: 列字段 + 类型字段（含 ref 字段 — 引擎落边）。
type nodeInput struct {
	Slug   string      `json:"slug"`
	Status int         `json:"status"`
	Sort   int         `json:"sort"`
	Fields core.Fields `json:"fields"`
}

func (b *backend) createNode(ctx *web.CmsCtx) {
	typ := ctx.Query("type")
	if typ == "" {
		ctx.Error(http.StatusBadRequest, "type required")
		return
	}
	var in nodeInput
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	id, err := b.core.CreateNode(&core.Node{
		Type: typ, Slug: in.Slug, Status: in.Status, Sort: in.Sort, Fields: in.Fields,
	})
	if err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id})
}

func (b *backend) getNode(ctx *web.CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	n, err := b.core.GetNodeById(id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if n == nil {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	// 管理视图: fields + ref 字段值（编辑回显）
	fields, err := b.core.FullFields(id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	n.Fields = fields
	_ = ctx.Json(http.StatusOK, n)
}

func (b *backend) updateNode(ctx *web.CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := b.core.GetNodeById(id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if existing == nil {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	var in nodeInput
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	// 全量语义: 未传的列保持现值
	if in.Slug == "" {
		in.Slug = existing.Slug
	}
	if in.Status == 0 && existing.Status != 0 {
		in.Status = existing.Status
	}
	n := &core.Node{
		ID: id, Type: existing.Type, Slug: in.Slug,
		Status: in.Status, Sort: in.Sort, Fields: in.Fields,
	}
	if err := b.core.UpdateNode(n); err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) deleteNode(ctx *web.CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	if err := b.core.DeleteNode(id); err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// changePassword 改密（先验旧密）。
func (b *backend) changePassword(ctx *web.CmsCtx) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	if !b.svc.VerifyPassword("admin", in.OldPassword) {
		ctx.Error(http.StatusUnauthorized, "old password incorrect")
		return
	}
	if err := b.svc.SetPassword(in.NewPassword); err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// tree 树视图数据: 某类型全部节点（FullFields 含 parent 引用值）, 不分页。
// 前端按自引用 ref 组装树; 树类型节点少（几十-几百）, 全量可接受。
func (b *backend) tree(ctx *web.CmsCtx) {
	typ := ctx.Query("type")
	if typ == "" {
		ctx.Error(http.StatusBadRequest, "type required")
		return
	}
	list, _, err := b.core.Q(core.ListQuery{Filter: `(= type {:t})`, Page: 1, Size: 10000},
		map[string]any{"t": typ})
	if err != nil {
		b.internal(ctx, err)
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, n := range list {
		fields, err := b.core.FullFields(n.ID)
		if err != nil {
			b.internal(ctx, err)
			return
		}
		items = append(items, map[string]any{
			"id": n.ID, "type": n.Type, "title": n.Title, "slug": n.Slug,
			"status": n.Status, "sort": n.Sort, "fields": fields,
		})
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": items})
}

// expand 引用展开预览（ExpandPath）: expr 为空 = 该类型全部 ref 字段一层全景。
func (b *backend) expand(ctx *web.CmsCtx) {
	nodeID := int64(ctx.QueryInt("node", 0))
	expr := ctx.Query("expr")
	if nodeID <= 0 {
		ctx.Error(http.StatusBadRequest, "node required")
		return
	}
	if expr == "" || expr == "*" {
		// 自动 / "*": 该类型所有 ref 字段（出边, 逗号并行, 一层）
		n, err := b.core.GetNodeById(nodeID)
		if err != nil {
			b.internal(ctx, err)
			return
		}
		if n == nil {
			ctx.Error(http.StatusNotFound, "not found")
			return
		}
		expr = "*" // 引擎语义: 该类型全部出边 ref 字段
	}
	root, err := b.core.ExpandPath(nodeID, expr)
	if err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"node": root})
}

// ── 站点配置（settings, cmx piece 语义）──────────

// listSettings 全部配置（可按分组过滤）。
func (b *backend) listSettings(ctx *web.CmsCtx) {
	group := ctx.Query("group")
	list, err := b.core.ListSettings(group)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": list})
}

// setSetting body 为 {"key","group","type"(编辑形态),"value"(自由 JSON)}。
func (b *backend) setSetting(ctx *web.CmsCtx) {
	var in struct {
		Key   string `json:"key"`
		Group string `json:"group"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	if err := b.core.SetSetting(in.Key, in.Group, in.Type, in.Value); err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) deleteSetting(ctx *web.CmsCtx) {
	if err := b.core.DeleteSetting(ctx.PathValue("key")); err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// ── 实体搜索（引用编辑器用）──────────────────────

// search 按标题/slug 模糊搜索节点（type 可选过滤）。
func (b *backend) search(ctx *web.CmsCtx) {
	q := strings.TrimSpace(ctx.Query("q"))
	typ := ctx.Query("type")
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 10)
	// Lisp 合成: type 可选过滤 + title/slug 模糊
	f := ""
	params := map[string]any{}
	if typ != "" {
		f = `(= type {:typ})`
		params["typ"] = typ
	}
	if q != "" {
		like := `(or (like title {:q}) (like slug {:q}))`
		if f != "" {
			f = `(and ` + f + ` ` + like + `)`
		} else {
			f = like
		}
		params["q"] = "%" + q + "%"
	}
	list, total, err := b.core.Q(core.ListQuery{Filter: f, Page: page, Size: size}, params)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": list, "total": total})
}
