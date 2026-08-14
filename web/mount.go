package web

import (
	"net/http"
)

// Mount 绑定全部前台路由（静态/上传/API/内容）— 目录配置空则跳过该项;
// 装配层一行调用, web 提供单一入口不暴露组件粒度。
func Mount(s *Site, static, uploads string) {
	// 静态资源（dir 空 = 跳过）
	if static != "" {
		s.static = static
		s.Get("/static/*", s.staticHandler())
	}
	// 上传文件服务（dir 空 = 跳过; admin 只管写入落盘）
	if uploads != "" {
		fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(uploads)))
		s.Get("/uploads/*", func(ctx *CmsCtx) { fs.ServeHTTP(ctx.W, ctx.R) })
	}
	// 记录 API（公开只读; Lisp filter + Q 直通）
	s.Get("/api/nodes/{type}", s.apiNodes)
	// 内容路由 + 404 统一出口
	s.Get("/node/{id}", s.nodeHandler())
	s.SetNotFound(func(ctx *CmsCtx) { s.render404(ctx) })
}
