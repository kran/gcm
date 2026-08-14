package web

import (
	"github.com/kran/gcm/core"
)

// PageDataMaker 页面上下文构造器（站点自定义形态 — gcm 不预设字段名）:
// 每个站点自己的 PageData（面包屑/导航高亮/查询参数/...）, 渲染前调用,
// 模板 .Page.X 访问站点自己定义的字段/方法。node 为 nil 表示非节点页
// （首页/搜索/404）。返回 any（泛型在 App 层包装, web 用 any）。
type PageDataMaker func(ctx *CmsCtx, node *core.Node) any
