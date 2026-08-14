package web

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
)

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
	fmt.Fprintf(ctx.W, `<!DOCTYPE html><html><head><title>Render Error</title></head><body style="font-family:monospace;padding:32px;background:#1a1a1a;color:#e5e7eb;">
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
