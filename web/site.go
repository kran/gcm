package web

import (
	"log"
	"net/http"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
)

// Site 站点装配: cho 路由 + 核心服务 + 渲染引擎 + 静态目录。
type Site struct {
	*cho.Cho[*CmsCtx]
	db     *dba.SQL
	svc    *core.Service
	eng    *render.Engine
	static string // 静态资源目录
	// Debug 开发模式: 渲染失败显示错误页（模板名/行号/原因/候选/数据 keys）;
	// 生产: HTML 注释（不泄漏细节, 仅日志）。
	Debug bool

	// PageDataMaker 页面上下文构造（站点自定义形态; nil = 无 Page 数据）—
	// 站点装配时提供（New 参数）; 返回类型任意（模板 .Page.X 访问）。
	PageDataMaker PageDataMaker
}

// Service 核心服务（站点代码查询入口: Q/图原语/Search; Setup 之外也可取）。
func (s *Site) Service() *core.Service { return s.svc }

// DB 暴露本站 db（站点项目/admin 用: 首次引导账号、业务表、逃生舱）。
func (s *Site) DB() *dba.SQL { return s.db }

// Func 注册站点自定义模板函数（转发到渲染引擎; 站点业务查询在此组装）。
func (s *Site) Func(name string, fn any) { s.eng.Func(name, fn) }

// Listen 监听并服务（单站直连）。
func (s *Site) Listen(addr string) error {
	log.Printf("gcm: listening on %s", addr)
	return http.ListenAndServe(addr, s)
}

// Handler HTTP 入口（组合/多站 mux.Add 用; 等价于 site 本身）。
func (s *Site) Handler() http.Handler { return s }

// New 建站点: cho + CmsCtx + 引擎引用。零挂载零注册 —
// 路由绑定（Mount*）与渲染事件（DefineRenderHooks）由装配层按序调用,
// web 只提供绑定原语不决定装配顺序。
func New(svc *core.Service, eng *render.Engine) *Site {
	s := &Site{
		db:  svc.DB(),
		svc: svc,
		eng: eng,
	}
	s.Cho = cho.New(func(w http.ResponseWriter, r *http.Request) *CmsCtx {
		return &CmsCtx{BaseContext: cho.MakeBaseContext(w, r), site: s}
	})
	return s
}
