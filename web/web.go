// Package web 站点 Web 层: 路由 + 渲染出口。
//
//	/static/* 静态资源 → 磁盘目录
//	/uploads/* 上传文件服务（装配层挂载）
//	/api/nodes/{type} 记录 API（Lisp filter + Q 直通）
//	/node/{id|slug} 节点详情（级联模板 node--{type}.html）
//	404 统一出口（404.html 或纯文本）
//
// 渲染失败 → HTML 注释（不泄漏错误细节给访客, 开发者查源码可见病灶）。
//
// 装配职责: web 只提供 Site 与路由原语 — 组件挂载（uploads/admin）与
// 业务钩子（Setup）由装配层（gcm.NewApp）按序调用。
package web

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/kran/gcm/core/hook"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
)

// HookRender 渲染事件（所有页面渲染前触发 — 站点注入页面级数据）:
// 原型 func(ctx *CmsCtx, data map[string]any) error —
// CmsCtx.Render 统一出口 Fire; 站点注入 Page 上下文/导航高亮等
// 页面级数据（节点页 + 自定义路由页全覆盖）。定义在 web 包
// （proto 带 CmsCtx, core 不引用 web）; 总线是 core 的。
const HookRender = "web.render"

// HookNodeRender 节点页渲染事件（节点级数据扩展）:
// 原型 func(ctx *CmsCtx, n *core.Node, data map[string]any) error —
// 内置 node 路由渲染前触发, 站点注入节点附加数据（Extra.url 等）。
// 页面级数据（Page 上下文）用 HookRender。
const HookNodeRender = "web.node.render"

// HookNodeEnrich 节点数据增强事件（节点页渲染前; 默认注入 url）:
// 原型 func(ctx *CmsCtx, n *core.Node) error — 站点 AddHook 覆盖默认 url
// 生成或追加附加数据（n.Extra["xxx"]）。默认实现生成 /node/{slug|id} url。
const HookNodeEnrich = "web.node.enrich"

// CmsCtx 请求上下文（富上下文: 携带站点引用, 自定义路由一行拿引擎 —
// 不再闭包捕获 site/svc/eng 三件套）。
type CmsCtx struct {
	*cho.BaseContext
	site *Site
}

// Service 核心服务（自定义路由查数据）。
func (c *CmsCtx) Service() *core.Service { return c.site.svc }

// Engine 渲染引擎（注册模板函数用）。
func (c *CmsCtx) Engine() *render.Engine { return c.site.eng }

// DB 底层数据库（业务表/逃生舱）。
func (c *CmsCtx) DB() *dba.SQL { return c.site.db }

// Render 站点渲染出口: 渲染前 Fire HookRender（站点注入页面级数据）,
// 候选级联 + 渲染错误 → HTML 注释（fail-loud 可见）。
// Page 上下文由站点 PageDataMaker 构造（未注入且 maker 非 nil 时）。
func (c *CmsCtx) Render(candidates []string, data map[string]any) {
	if err := c.site.svc.Hooks().Fire(HookRender, c, data); err != nil {
		c.site.renderError(c, "render hook failed: "+err.Error())
		return
	}
	// 页面上下文（站点 PageDataMaker 构造; nil maker = 无 Page）
	if _, ok := data["Page"]; !ok && c.site.PageDataMaker != nil {
		var node *core.Node
		if n, ok := data["Node"].(*core.Node); ok {
			node = n
		}
		data["Page"] = c.site.PageDataMaker(c, node)
	}
	c.site.renderHTML(c, candidates, data)
}

// Site 站点装配: cho 路由 + 核心服务 + 渲染引擎 + 静态目录。
type Site struct {
	*cho.Cho[*CmsCtx]
	db     *dba.SQL
	svc    *core.Service
	eng    *render.Engine
	static string // 静态资源目录
	// Debug 开发模式: 渲染失败显示错误页（模板名/行号/原因/候选/数据 keys）;
	// 生产: HTML 注释（不泄漏细节, 仅日志）。
	Debug bool

	// PageDataMaker 页面上下文构造（站点自定义形态; nil = 无 Page 数据）—
	// 站点装配时提供（New 参数）; 返回类型任意（模板 .Page.X 访问）。
	PageDataMaker PageDataMaker
}

// Service 核心服务（站点代码查询入口: Q/图原语/Search; Setup 之外也可取）。
func (s *Site) Service() *core.Service { return s.svc }

// DB 暴露本站 db（站点项目/admin 用: 首次引导账号、业务表、逃生舱）。
func (s *Site) DB() *dba.SQL { return s.db }

// Func 注册站点自定义模板函数（转发到渲染引擎; 站点业务查询在此组装）。
func (s *Site) Func(name string, fn any) { s.eng.Func(name, fn) }

// New 建站点: cho + CmsCtx + 引擎引用。零挂载零注册 —
// 路由绑定（Mount*）与渲染事件（DefineRenderHooks）由装配层按序调用,
// web 只提供绑定原语不决定装配顺序。
func New(svc *core.Service, eng *render.Engine) *Site {
	s := &Site{
		db:  svc.DB(),
		svc: svc,
		eng: eng,
	}
	s.Cho = cho.New(func(w http.ResponseWriter, r *http.Request) *CmsCtx {
		return &CmsCtx{BaseContext: cho.MakeBaseContext(w, r), site: s}
	})
	return s
}

// DefineRenderHooks 声明渲染事件（注册即校验签名 — 站点 AddHook 前必须存在;
// 装配层调用）。proto 带 CmsCtx, 定义必须在 web 包。
func DefineRenderHooks(svc *core.Service) error {
	return svc.Hooks().Define(
		hook.Spec{Name: HookRender, Proto: func(*CmsCtx, map[string]any) error { return nil }},
		hook.Spec{Name: HookNodeRender, Proto: func(*CmsCtx, *core.Node, map[string]any) error { return nil }},
		hook.Spec{Name: HookNodeEnrich, Proto: func(*CmsCtx, *core.Node) error { return nil }},
	)
}

// MountStatic 绑定 /static/* → 磁盘目录（dir 空 = 跳过）。
func MountStatic(s *Site, dir string) {
	if dir == "" {
		return
	}
	s.static = dir
	s.Get("/static/*", s.staticHandler())
}

// MountUploads 绑定 /uploads/* 上传文件服务（上传目录是站点级配置 —
// 前台资源服务不依赖 admin 是否启用; admin 只管写入落盘）。
func MountUploads(s *Site, dir string) {
	if dir == "" {
		return
	}
	fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(dir)))
	s.Get("/uploads/*", func(ctx *CmsCtx) { fs.ServeHTTP(ctx.W, ctx.R) })
}

// MountAPI 绑定记录 API（公开只读; Lisp filter + Q 直通）。
func MountAPI(s *Site) {
	s.Get("/api/nodes/{type}", s.apiNodes)
}

// MountContent 绑定内容路由（/node/{id|slug}）+ 404 统一出口。
func MountContent(s *Site) {
	s.Get("/node/{id}", s.nodeHandler())
	s.SetNotFound(func(ctx *CmsCtx) { s.render404(ctx) })
}

// ── 渲染出口 ─────────────────────────────────────

// renderHTML 渲染模板; 失败: Debug 显示错误页（500 + 详情）; 生产 HTML 注释。
func (s *Site) renderHTML(ctx *CmsCtx, candidates []string, data map[string]any) {
	var buf bytes.Buffer
	if err := s.eng.Render(&buf, candidates, data); err != nil {
		slog.Error("render failed", "path", ctx.R.URL.Path, "err", err)
		s.renderErrorDetail(ctx, err, candidates, data)
		return
	}
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	_, _ = ctx.W.Write(buf.Bytes())
}

// renderErrorDetail 渲染失败出口: Debug → 500 错误页; 生产 → HTML 注释。
func (s *Site) renderErrorDetail(ctx *CmsCtx, err error, candidates []string, data map[string]any) {
	if !s.Debug {
		ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(ctx.W, "<!-- render error: %s -->", htmlCommentSafe(err.Error()))
		return
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	ctx.W.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(ctx.W, `
		<!DOCTYPE html><html>
		<head><title>Render Error</title></head>
		<body style="font-family:monospace;padding:32px;background:#1a1a1a;color:#e5e7eb;">
		<h1 style="color:#f87171;">Render Error</h1>
		<h3 style="color:#9ca3af;">%s %s</h3>
		<pre style="background:#111;padding:16px;border-radius:8px;color:#fbbf24;overflow:auto;">%s</pre>
		<h4>候选模板</h4><pre style="background:#111;padding:12px;border-radius:8px;color:#93c5fd;">%s</pre>
		<h4>数据 keys</h4><pre style="background:#111;padding:12px;border-radius:8px;color:#a7f3d0;">%s</pre>
		</body></html>`, ctx.R.Method, ctx.R.URL.Path, html.EscapeString(err.Error()),
		html.EscapeString(strings.Join(candidates, " → ")), html.EscapeString(strings.Join(keys, ", ")))
}

// renderError 渲染失败出口: HTML 注释（fail-loud 可见）。
func (s *Site) renderError(ctx *CmsCtx, msg string) {
	slog.Error("render failed", "path", ctx.R.URL.Path, "err", msg)
	s.renderErrorDetail(ctx, errors.New(msg), nil, nil)
}

// render404 统一 404 出口: 有 404.html 渲染之, 否则纯文本。
// 渲染前 Fire HookRender（404 页也需要页面上下文 — 导航高亮等）。
// 404 是常态 — 渲染失败一律回退默认文本, 不 fail loud。
func (s *Site) render404(ctx *CmsCtx) {
	var buf bytes.Buffer
	data := map[string]any{"Path": ctx.R.URL.Path}
	_ = s.svc.Hooks().Fire(HookRender, ctx, data) // 404 上下文失败不阻断 404 页
	err := s.eng.Render(&buf, []string{"404.html"}, data)
	if err == nil {
		ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
		ctx.W.WriteHeader(http.StatusNotFound)
		_, _ = ctx.W.Write(buf.Bytes())
		return
	}
	ctx.String(http.StatusNotFound, "404 page not found")
}

// htmlCommentSafe 防注释内容含 "--" 或 "-->" 破坏 HTML 结构。
func htmlCommentSafe(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "-->", "- ->"), "--", "- -")
}

// ── 路由 handler ─────────────────────────────────

// nodeHandler /node/{id|slug}: 纯数字按 id, 否则按 slug。
// 不存在/未发布 → 404。
func (s *Site) nodeHandler() func(ctx *CmsCtx) {
	return func(ctx *CmsCtx) {
		raw := ctx.PathValue("id")
		var n *core.Node
		var err error
		if id, e := strconv.ParseInt(raw, 10, 64); e == nil {
			n, err = s.svc.GetNodeById(id)
		} else {
			n, err = s.svc.GetNodeBySlug(raw)
		}
		if err != nil {
			slog.Error("node lookup failed", "path", raw, "err", err)
			ctx.String(http.StatusInternalServerError, "500 internal server error")
			return
		}
		if n == nil || n.Status != core.StatusPublished {
			s.render404(ctx)
			return
		}
		data := map[string]any{"Node": n, "ID": n.ID}
		// 节点数据增强: 站点 hook 优先; 未注入 url 时默认按 TypeDef.URL 生成
		if err := s.svc.Hooks().Fire(HookNodeEnrich, ctx, n); err != nil {
			slog.Error("node enrich hook failed", "path", raw, "err", err)
			ctx.String(http.StatusInternalServerError, "500 internal server error")
			return
		}
		if n.Extra == nil {
			n.Extra = map[string]any{}
		}
		if _, ok := n.Extra["url"]; !ok {
			n.Extra["url"] = n.URL() // Node.URL — 默认路由约定, 站点可覆盖
		}
		if err := s.svc.Hooks().Fire(HookNodeRender, ctx, n, data); err != nil {
			slog.Error("node render hook failed", "path", raw, "err", err)
			ctx.String(http.StatusInternalServerError, "500 internal server error")
			return
		}
		// 统一走 Render（Fire HookRender — 页面级数据注入与自定义路由一致）
		ctx.Render(render.Candidates(n), data)
	}
}

// staticHandler /static/* → 磁盘目录。
func (s *Site) staticHandler() func(ctx *CmsCtx) {
	fs := http.StripPrefix("/static", http.FileServer(http.Dir(s.static)))
	return func(ctx *CmsCtx) {
		fs.ServeHTTP(ctx.W, ctx.R)
	}
}
