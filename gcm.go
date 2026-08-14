// Package gcm 实体-关系 CMS（从零构想）。
//
// 核心概念只有两个: 实体（nodes）与引用（edges），一切差异都在类型定义里。
// 设计文档见 design.md；实施计划见 TODO.md。
//
// 装配层（PocketBase 形态的库）:
//
//	app, err := gcm.NewApp(gcm.Options{}, gcm.SiteSpec{
//		Hosts:     []string{"zhiqi.com"},
//		DBPath:    "data/zhiqi.db",
//		Types:     typesYAML,
//		Templates: "zhiqi/templates",
//		Setup:     func(s *web.Site, svc *core.Service) error { ... },
//	})
//	log.Fatal(app.Listen(":8080"))
//
// 多站点按域名分发（HostMux）; 站点业务（路由/模板函数/seed）在 Setup
// 钩子里写 Go 代码 — 库装配骨架, 业务不进配置。
package gcm

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
	"github.com/kran/gcm/web"
	"github.com/kran/gcm/web/admin"
)

// SiteSpec 站点装配规格: 骨架配置 + 业务钩子。
// T 是站点 PageData 泛型（cho 的 CtxMaker[T] 模式 — 站点定形状, gcm 不预设字段名）。
type SiteSpec[T any] struct {
	Hosts  []string // 域名列表（Host 头分发键; 多站时必须非空, 单站可空）
	DBPath string   // SQLite 库文件路径
	Types  []byte   // types.yaml 内容（类型定义, 站点差异所在）
	// Kinds 站点自定义 kind（在 types.Load 之前注册 — 类型定义里用到
	// 自定义 kind 才能通过校验）。完整扩展闭环: Go 值校验 + Editor 原语
	// 声明 + 站点挂载控件组件路由（/admin/ui-extras/{widget}.vue）。
	Kinds     []types.Kind
	Templates string // 模板目录（node--{type}.html 级联根）
	Static    string // 静态资源目录（可空 = 不挂 /static）
	Uploads   string // 上传目录（可空 = 不上传; 装配层挂 /uploads 服务 + admin 写入）
	// PageDataMaker 页面上下文构造（站点自定义形态; nil = 无 Page 数据）—
	// 模板 .Page.X 访问站点自己定义的字段/方法。
	PageDataMaker func(svc *core.Service, ctx *web.CmsCtx, node *core.Node) T
	// Setup 站点业务装配: 自定义路由 / 模板函数 / seed。装配后调用;
	// 返回 error = 装配失败（fail-loud）。
	Setup func(s *web.Site, svc *core.Service) error
}

// Options 应用级选项。
type Options struct {
	// AdminPass 管理后台固定密码（空 = 首次生成随机密码并打印）。
	AdminPass string
	// Debug 开发模式: 渲染失败显示错误页（模板名/行号/原因/候选/数据 keys）;
	// 生产空值 = HTML 注释（不泄漏细节）。
	Debug bool
}

// App 多站点应用: HostMux 按域名分发到各站点。
type App[T any] struct {
	site    *web.Site
	options Options
}

// NewApp 装配**一个**站点。每个站点一个 App 实例（各自 PageData 类型 T —
// 多站点类型不同各自 NewApp）。返回 *App[T]; Handler() 供开发者组合挂载
// （web.HostMux 是可选组合件 — 多站由开发者自己 mux.Add 挂载）。
func NewApp[T any](opts Options, spec SiteSpec[T]) (*App[T], error) {
	app := &App[T]{options: opts}
	site, err := app.build(spec)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	app.site = site
	return app, nil
}

// build 单站点装配 — 按层分节（cmx 风格: 每层一个小函数, 顺序执行）:
//
//	① 存储    openDB + 迁移 + admin 账号引导
//	② 类型    kinds 注册 + types.yaml 加载
//	③ 引擎    core.Service + render.Engine
//	④ Web     web.New（站点形态选项）
//	⑤ 组件    uploads 服务 + admin 挂载（参数从 site 上下文取）
//	⑥ 业务    Setup 钩子（站点路由/模板函数/seed）
func (a *App[T]) build(spec SiteSpec[T]) (*web.Site, error) {
	db, err := a.openDB(spec)
	if err != nil {
		return nil, err
	}
	ts, err := a.loadTypes(spec)
	if err != nil {
		return nil, err
	}
	svc := core.New(db, ts)
	eng := render.New(spec.Templates, svc)

	// PageDataMaker 泛型 T → any 包装（web 用 any, 类型在 App 层）
	var maker web.PageDataMaker
	if spec.PageDataMaker != nil {
		maker = func(ctx *web.CmsCtx, n *core.Node) any { return spec.PageDataMaker(svc, ctx, n) }
	}
	site := web.New(svc, eng, web.SiteOptions{
		Static: spec.Static,
		Maker:  maker,
		Debug:  a.options.Debug,
	})

	// 组件: uploads（前台资源服务）+ admin（写入/管理）— 参数从 site 上下文取
	site.MountUploads(spec.Uploads)
	admin.Mount(site, spec.Uploads)

	// 业务钩子: 站点路由/模板函数/seed
	if spec.Setup != nil {
		if err := spec.Setup(site, svc); err != nil {
			return nil, fmt.Errorf("setup: %w", err)
		}
	}
	return site, nil
}

// openDB ① 存储: 打开 + SQL 日志 + 迁移 + admin 账号引导。
func (a *App[T]) openDB(spec SiteSpec[T]) (*dba.SQL, error) {
	db, err := dba.Open("sqlite", spec.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db = db.SetLogger(dba.NewLogger(slog.Default(), time.Second*1, false))
	if err := migrations.Up(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// 管理账号引导（固定密码优先, 否则随机打印一次）
	if dc, err := admin.EnsureDefaults(db); err != nil {
		return nil, fmt.Errorf("admin bootstrap: %w", err)
	} else if dc != nil {
		log.Printf("gcm: site (%v): admin created: %s / %s", spec.Hosts, dc.Username, dc.Password)
		if a.options.AdminPass != "" {
			if err := admin.NewService(db).SetPassword(a.options.AdminPass); err != nil {
				return nil, fmt.Errorf("set admin password: %w", err)
			}
			log.Printf("gcm: site (%v): admin password set to fixed", spec.Hosts)
		}
	}
	return db, nil
}

// loadTypes ② 类型: 站点自定义 kind 注册（types.Load 前）+ types.yaml。
func (a *App[T]) loadTypes(spec SiteSpec[T]) (*types.Types, error) {
	ts := types.New()
	for _, k := range spec.Kinds {
		ts.RegisterKind(k)
	}
	if len(spec.Types) > 0 {
		if err := ts.Load(spec.Types); err != nil {
			return nil, fmt.Errorf("types: %w", err)
		}
	}
	return ts, nil
}

// Handler 应用入口（测试/嵌入用）— 单站; 多站组合用 web.HostMux.Add(hosts, a.Handler())。
func (a *App[T]) Handler() http.Handler { return a.site }

// Site 站点实例（多站组合/高级装配用）。
func (a *App[T]) Site() *web.Site { return a.site }

// Listen 监听并服务（单站直连; 多站请用 Handler + HostMux）。
func (a *App[T]) Listen(addr string) error {
	log.Printf("gcm: listening on %s", addr)
	return http.ListenAndServe(addr, a.site)
}
