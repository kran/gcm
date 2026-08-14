package web

import (
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/hook"
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

// DefineRenderHooks 声明渲染事件（注册即校验签名 — 站点 AddHook 前必须存在;
// 装配层调用）。proto 带 CmsCtx, 定义必须在 web 包。
func DefineRenderHooks(svc *core.Service) error {
	if err := svc.Hooks().Define(
		hook.Spec{Name: HookRender, Proto: func(*CmsCtx, map[string]any) error { return nil }},
		hook.Spec{Name: HookNodeRender, Proto: func(*CmsCtx, *core.Node, map[string]any) error { return nil }},
		hook.Spec{Name: HookNodeEnrich, Proto: func(*CmsCtx, *core.Node) error { return nil }},
	); err != nil {
		return err
	}
	// 默认 handler（内部消费示范 — hook 的正确用法）: 节点页 url 注入;
	// 站点 AddHook(HookNodeRender, ...) 可覆盖 Extra["url"] 或注入其他。
	return svc.Hooks().AddHook(HookNodeRender, func(ctx *CmsCtx, n *core.Node, data map[string]any) error {
		if n.Extra == nil {
			n.Extra = map[string]any{}
		}
		if _, ok := n.Extra["url"]; !ok {
			n.Extra["url"] = n.URL()
		}
		return nil
	})
}
