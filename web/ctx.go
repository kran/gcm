package web

import (
	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
)

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
