package web

import (
	"net/http"
)

// MountStatic 绑定 /static/* → 磁盘目录（dir 空 = 跳过）。
func MountStatic(s *Site, dir string) {
	if dir == "" {
		return
	}
	s.static = dir
	s.Get("/static/*", s.staticHandler())
}

// MountUploads 绑定 /uploads/* 上传文件服务（上传目录是站点级配置 —
// 前台资源服务不依赖 admin 是否启用; admin 只管写入落盘）。
func MountUploads(s *Site, dir string) {
	if dir == "" {
		return
	}
	fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(dir)))
	s.Get("/uploads/*", func(ctx *CmsCtx) { fs.ServeHTTP(ctx.W, ctx.R) })
}

// MountAPI 绑定记录 API（公开只读; Lisp filter + Q 直通）。
func MountAPI(s *Site) {
	s.Get("/api/nodes/{type}", s.apiNodes)
}

// MountContent 绑定内容路由（/node/{id|slug}）+ 404 统一出口。
func MountContent(s *Site) {
	s.Get("/node/{id}", s.nodeHandler())
	s.SetNotFound(func(ctx *CmsCtx) { s.render404(ctx) })
}
