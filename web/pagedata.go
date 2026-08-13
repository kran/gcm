package web

// PageDataMaker 页面上下文构造器（站点自定义形态 — gcm 不预设字段名）:
// 每个站点自己的 PageData（面包屑/导航高亮/查询参数/...）, 模板 .Page.X 访问
// 站点自己定义的字段/方法。node 为 nil 表示非节点页（首页/搜索/404）。

import (
	"github.com/kran/gcm/core"
)

// PageDataMaker 页面上下文构造: 渲染前调用, 返回站点自定义 PageData（any）。
type PageDataMaker func(ctx *CmsCtx, node *core.Node) any
