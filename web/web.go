// Package web 站点 Web 层: 路由绑定原语 + 渲染出口。
//
// 文件划分（每文件一个职能）:
//
//	site.go    Site 结构 + New + 引擎引用方法
//	ctx.go     CmsCtx 请求上下文 + Render 出口
//	hooks.go   渲染事件常量 + DefineRenderHooks（装配层调用）
//	mount.go   路由绑定原语（MountStatic/MountUploads/MountAPI/MountContent）
//	routes.go  路由 handler（nodeHandler/staticHandler）
//	render.go  渲染出口（renderHTML/render404/错误出口）
//	api.go     记录 API（/api/nodes/{type} — Lisp filter + Q）
//	hostmux.go HostMux 多站分发
//	pagedata.go PageDataMaker 定义
//
// 装配职责: web 只提供绑定原语 — 组件挂载（uploads/admin）与业务钩子
// （Setup）由装配层（gcm.NewApp）按序调用; web 不决定装配顺序。
//
// 渲染失败 → HTML 注释（不泄漏错误细节给访客, 开发者查源码可见病灶）。
package web
