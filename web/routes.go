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
	// 默认 url 注入是 HookNodeRender 的默认 handler（DefineRenderHooks 注册 —
	// 站点可加自己的 handler 覆盖 Extra["url"] 或注入其他）
	if err := s.svc.Hooks().Fire(HookNodeRender, ctx, n, data); err != nil {
		slog.Error("node render hook failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	// 模板候选: 默认级联 + HookCandidates 站点可自定义（可变参数）
	candidates := render.Candidates(n)
	if err := s.svc.Hooks().Fire(HookCandidates, ctx, n, &candidates); err != nil {
		slog.Error("candidates hook failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	// 统一走 Render（Fire HookRender — 页面级数据注入与自定义路由一致）
	ctx.Render(candidates, data)
}
