// Package web 站点 Web 层: 路由 + 渲染出口。
//
// M7 单站点起步（多站点 hostmux 后续）:
//
//	/static/* 静态资源 → 磁盘目录
//	/node/{id|slug} 节点详情（级联模板 node--{type}.html）
//	404 统一出口（404.html 或纯文本）
//
// 渲染失败 → HTML 注释（不泄漏错误细节给访客, 开发者查源码可见病灶）。
package web

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcm/core/hook"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
)

// HookNodeRender 节点页渲染事件（站点扩展注入渲染数据）:
// 原型 func(ctx *CmsCtx, n *core.Node, data map[string]any) error —
// 内置 node 路由渲染前触发, 站点往 data 里注入页面上下文/附加数据。
// 定义在 web 包（proto 带 CmsCtx, core 不引用 web）; 总线是 core 的。
const HookNodeRender = "web.node.render"

// CmsCtx 请求上下文（富上下文: 携带站点引用, 自定义路由一行拿引擎 —
// 不再闭包捕获 site/svc/eng 三件套）。
type CmsCtx struct {
	*cho.BaseContext
	site *Site
}

// Svc 核心服务（自定义路由查数据）。
func (c *CmsCtx) Svc() *core.Service { return c.site.svc }

// Engine 渲染引擎（注册模板函数用）。
func (c *CmsCtx) Engine() *render.Engine { return c.site.eng }

// DB 底层数据库（业务表/逃生舱）。
func (c *CmsCtx) DB() *dba.SQL { return c.site.db }

// Render 站点渲染出口: 候选级联 + 渲染错误 → HTML 注释（fail-loud 可见）。
func (c *CmsCtx) Render(candidates []string, data map[string]any) {
	c.site.renderHTML(c, candidates, data)
}

// Site 站点装配: cho 路由 + 核心服务 + 渲染引擎 + 静态目录。
type Site struct {
	*cho.Cho[*CmsCtx]
	db     *dba.SQL
	svc    *core.Service
	eng    *render.Engine
	static string // 静态资源目录
}

// DB 暴露本站 db（站点项目/admin 用: 首次引导账号、业务表）。
func (s *Site) DB() *dba.SQL { return s.db }

// Func 注册站点自定义模板函数（转发到渲染引擎; 站点业务查询在此组装）。
func (s *Site) Func(name string, fn any) { s.eng.Func(name, fn) }

// New 建站点。static 是静态资源目录（可为空串 = 不挂静态路由）。
func New(svc *core.Service, eng *render.Engine, static string) *Site {
	s := &Site{
		db:     svc.DB(),
		svc:    svc,
		eng:    eng,
		static: static,
	}
	s.Cho = cho.New(func(w http.ResponseWriter, r *http.Request) *CmsCtx {
		return &CmsCtx{BaseContext: cho.MakeBaseContext(w, r), site: s}
	})
	// 声明渲染事件（注册即校验签名; 站点 AddHook 前必须存在）
	if err := svc.Hooks().Define(hook.Spec{Name: HookNodeRender,
		Proto: func(*CmsCtx, *core.Node, map[string]any) error { return nil }}); err != nil {
		panic("web: define HookNodeRender: " + err.Error())
	}
	s.mount()
	return s
}

// MountUploads 挂载 /uploads/* 上传文件服务（admin 上传落盘目录）。
func (s *Site) MountUploads(dir string) {
	if dir == "" {
		return
	}
	fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(dir)))
	s.Get("/uploads/*", func(ctx *CmsCtx) { fs.ServeHTTP(ctx.W, ctx.R) })
}

// mount 挂载前台路由。
func (s *Site) mount() {
	if s.static != "" {
		s.Get("/static/*", s.staticHandler())
	}
	s.Get("/node/{id}", s.nodeHandler())
	// 路由未匹配 → 同一 404 出口
	s.SetNotFound(func(ctx *CmsCtx) { s.render404(ctx) })
}

// ── 渲染出口 ─────────────────────────────────────

// renderHTML 渲染模板; 失败输出 HTML 注释（不泄漏细节, 日志记录）。
func (s *Site) renderHTML(ctx *CmsCtx, candidates []string, data map[string]any) {
	var buf bytes.Buffer
	if err := s.eng.Render(&buf, candidates, data); err != nil {
		slog.Error("render failed", "path", ctx.R.URL.Path, "err", err)
		ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(ctx.W, "<!-- render error: %s -->", htmlCommentSafe(err.Error()))
		return
	}
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	_, _ = ctx.W.Write(buf.Bytes())
}

// render404 统一 404 出口: 有 404.html 渲染之, 否则纯文本。
// 404 是常态 — 渲染失败一律回退默认文本, 不 fail loud。
func (s *Site) render404(ctx *CmsCtx) {
	var buf bytes.Buffer
	err := s.eng.Render(&buf, []string{"404.html"}, map[string]any{
		"Path": ctx.R.URL.Path,
	})
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
			n, err = s.svc.Get(id)
		} else {
			n, err = s.svc.GetBySlug(raw)
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
		if err := s.svc.Hooks().Fire(HookNodeRender, ctx, n, data); err != nil {
			slog.Error("node render hook failed", "path", raw, "err", err)
			ctx.String(http.StatusInternalServerError, "500 internal server error")
			return
		}
		s.renderHTML(ctx, render.Candidates(n), data)
	}
}

// staticHandler /static/* → 磁盘目录。
func (s *Site) staticHandler() func(ctx *CmsCtx) {
	fs := http.StripPrefix("/static", http.FileServer(http.Dir(s.static)))
	return func(ctx *CmsCtx) {
		fs.ServeHTTP(ctx.W, ctx.R)
	}
}
