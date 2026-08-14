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
	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
	"github.com/kran/gcm/web"
	"github.com/kran/gcm/web/admin"
	"log"
)

// SiteSpec 站点装配规格: 骨架配置 + 业务钩子（全部站点级 — 无应用级 Options）。
// T 是站点 PageData 泛型（cho 的 CtxMaker[T] 模式 — 站点定形状, gcm 不预设字段名）。
type SiteSpec[T any] struct {
	Hosts  []string // 域名列表（Host 头分发键; 多站时必须非空, 单站可空）
	DBPath string   // SQLite 库文件路径
	Types  []byte   // types.yaml 内容（类型定义, 站点差异所在）
	// AdminPass 管理后台固定密码（空 = 首次生成随机密码并打印）。
	AdminPass string
	// Debug 开发模式: 渲染失败显示错误页（模板名/行号/原因/候选/数据 keys）;
	// 生产空值 = HTML 注释（不泄漏细节）。
	Debug bool
	// SQLLogger dba SQL 日志器（nil = 默认: slog.Default 慢查询 1s 阈值）—
	// 构造用 dba.NewLogger(logger, threshold, clean) 或任意 LogFunc。
	SQLLogger dba.LogFunc
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

// 多站组合: HostMux 重导出（gcm 门面提供; 装配层与站点解耦 —
// 各自 NewSite 装配, 再 mux.Add(hosts, site) 组合）。
type HostMux = web.HostMux

// NewHostMux 建多站分发器（按 Host 头分发; 未知 host 走 fallback）。
func NewHostMux() *HostMux { return web.NewHostMux() }

// builder 装配器（内部 — NewSite 的工作单元）。
type builder[T any] struct{}

// NewSite 装配**一个**站点, 返回 *web.Site（泛型 T 只在装配期 —
// PageDataMaker 类型; 运行时是 *web.Site, Listen/Handler 直接可用）。
// 多站: 各自 NewSite, 再 gcm.NewHostMux() + mux.Add(hosts, site) 组合。
func NewSite[T any](spec SiteSpec[T]) (*web.Site, error) {
	b := &builder[T]{}
	site, err := b.build(spec)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return site, nil
}

// build 单站点装配 — 按层分节（cmx 风格: 每层一个小函数, 顺序执行）:
//
//	① 存储    openDB + 迁移 + admin 账号引导
//	② 类型    kinds 注册 + types.yaml 加载
//	③ 引擎    core.Service + render.Engine
//	④ Web     web.New（站点形态选项）
//	⑤ 组件    uploads 服务 + admin 挂载（参数从 site 上下文取）
//	⑥ 业务    Setup 钩子（站点路由/模板函数/seed）
func (a *builder[T]) build(spec SiteSpec[T]) (*web.Site, error) {
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
	site := web.New(svc, eng)
	site.Debug = spec.Debug
	site.PageDataMaker = maker

	// 装配序列（每项一个绑定原语, 顺序即职责）:
	//   hook 注册 → 静态/上传 → API → 内容路由 → admin → 业务 Setup
	if err := web.DefineRenderHooks(svc); err != nil {
		return nil, fmt.Errorf("define render hooks: %w", err)
	}
	web.Mount(site, spec.Static, spec.Uploads)
	admin.Mount(site, spec.Uploads)
	if spec.Setup != nil {
		if err := spec.Setup(site, svc); err != nil {
			return nil, fmt.Errorf("setup: %w", err)
		}
	}
	return site, nil
}

// openDB ① 存储: 打开 + SQL 日志 + 迁移 + admin 账号引导。
func (a *builder[T]) openDB(spec SiteSpec[T]) (*dba.SQL, error) {
	db, err := dba.Open("sqlite", spec.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQL 日志: 站点自定义优先, 否则默认（慢查询 1s 阈值, slog.Default）
	if spec.SQLLogger != nil {
		db = db.SetLogger(spec.SQLLogger)
	}

	if err := migrations.Up(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// 管理账号引导（固定密码优先, 否则随机打印一次）
	if dc, err := admin.EnsureDefaults(db); err != nil {
		return nil, fmt.Errorf("admin bootstrap: %w", err)
	} else if dc != nil {
		log.Printf("gcm: site (%v): admin created: %s / %s", spec.Hosts, dc.Username, dc.Password)
		if spec.AdminPass != "" {
			if err := admin.NewService(db).SetPassword(spec.AdminPass); err != nil {
				return nil, fmt.Errorf("set admin password: %w", err)
			}
			log.Printf("gcm: site (%v): admin password set to fixed", spec.Hosts)
		}
	}
	return db, nil
}

// loadTypes ② 类型: 站点自定义 kind 注册（types.Load 前）+ types.yaml。
func (a *builder[T]) loadTypes(spec SiteSpec[T]) (*types.Types, error) {
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
