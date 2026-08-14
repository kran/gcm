package web

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
)

// ── 路由 handler ─────────────────────────────────

// nodeHandler /node/{id|slug}: 纯数字按 id, 否则按 slug。
// 不存在/未发布 → 404。
func (s *Site) nodeHandler(ctx *CmsCtx) {
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
	// 节点数据增强: 站点 hook 优先; 未注入 url 时默认 Node.URL()
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

// staticHandler /static/* → 磁盘目录。
func (s *Site) staticHandler() func(ctx *CmsCtx) {
	fs := http.StripPrefix("/static", http.FileServer(http.Dir(s.static)))
	return func(ctx *CmsCtx) {
		fs.ServeHTTP(ctx.W, ctx.R)
	}
}
