package web

import (
	"net/http"
)

// Mount 绑定全部前台路由（静态/上传/API/内容）— 目录配置空则跳过该项;
// 装配层一行调用, web 提供单一入口不暴露组件粒度。
func Mount(s *Site, static, uploads string) {
	// 静态资源（dir 空 = 跳过; 带 ?w=/h=/mode= 参数 = 图片裁剪, 同 uploads）
	if static != "" {
		s.static = static
		fs := http.StripPrefix("/static", http.FileServer(http.Dir(static)))
		s.Get("/static/*", func(ctx *CmsCtx) { serveImg(static, "/static/", ctx, fs) })
	}
	// 上传文件服务（dir 空 = 跳过; admin 只管写入落盘）
	// 带 ?w=/h=/mode= 参数 = 图片裁剪（OSS 风格, 磁盘缓存）; 无参数 = 原图直出
	if uploads != "" {
		fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(uploads)))
		s.Get("/uploads/*", func(ctx *CmsCtx) { serveImg(uploads, "/uploads/", ctx, fs) })
	}
	// 记录 API（公开只读; Lisp filter + Q 直通）
	s.Get("/api/nodes/{type}", s.apiNodes)
	// 内容路由 + 404 统一出口
	s.Get("/node/{id}", s.nodeHandler)
	s.SetNotFound(func(ctx *CmsCtx) { s.render404(ctx) })
}
