// gcm 示例站点: 展示库的组装方式（站点项目形态）。
//
//	装配: db + 迁移 + 类型系统 + 核心引擎 + 渲染引擎 + web + admin
//	seed: 分类树 / 专家 / 文章（引用互连）/ 任职关系节点
//	路由: / 首页, /category/{slug} 分类页, /node/{id|slug} 内置
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/kran/gcm"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
	"github.com/kran/gcm/web"
)

// adminPass 固定管理密码（测试方便）; 空 = 随机生成打印一次。
var adminPass = flag.String("admin-pass", "cmx12345", "固定后台密码 (空 = 随机生成)")

func main() {
	flag.Parse()
	yaml, err := os.ReadFile("types.yaml")
	if err != nil {
		log.Fatal(err)
	}
	// PocketBase 形态: 一个 App 装配全部（db/迁移/类型/引擎/admin/账号引导）
	app, err := gcm.NewApp(gcm.Options{AdminPass: *adminPass}, gcm.SiteSpec{
		Hosts:     []string{"localhost", "127.0.0.1"},
		DBPath:    "example.db",
		Types:     yaml,
		Templates: "templates",
		Static:    "static",
		Uploads:   "uploads",
		Setup:     setup,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(app.Listen(":8080"))
}

// setup 站点业务: seed + 模板函数 + 自定义路由（Setup 钩子里写 Go 代码）。
func setup(site *web.Site, svc *core.Service) error {
	if err := seed(svc); err != nil {
		return err
	}

	// 导航高亮: 当前节点的高亮分类集合（所属分类 + 全部祖先）。
	// 引擎原语组装（outRefs 找分类 / traverse 祖先链）, 模板只做 in 判断。
	// 分类页 = 自身 + parent 链; 内容页 = 所属分类（单叶语义取第一条）+ 其链。
	site.Func("activeChain", func(n *core.Node) []int64 {
		if n == nil {
			return nil
		}
		var catID int64
		if n.Type == "category" {
			catID = n.ID
		} else {
			cats, _, err := svc.OutRefs(n.ID, "categories", 1, 5)
			if err != nil || len(cats) == 0 {
				return nil
			}
			catID = cats[0].ToNode
		}
		chain, err := svc.Traverse(catID, "parent", 50)
		if err != nil {
			return nil
		}
		return append([]int64{catID}, chain...)
	})

	// 分类子树文章: Subtree 集合 → filter 数组（categories ~ {:ids}）→ 列表。
	// 模板一行调用（node--category.html / 任何分类上下文）; 不依赖路由注入。
	site.Func("articlesOf", func(catID int64) []core.Node {
		ids, err := subtreeIDs(svc, catID) // 含自身（Subtree 本身不含起点）
		if err != nil {
			return nil
		}
		anyIDs := make([]any, len(ids))
		for i, id := range ids {
			anyIDs[i] = id
		}
		cf, err := svc.CompileFilter(`categories ~ {:ids}`)
		if err != nil {
			return nil
		}
		where, args, err := svc.BuildFilter(cf, "article", map[string]any{"ids": anyIDs})
		if err != nil {
			return nil
		}
		list, _, err := svc.ListFiltered("article", where, args, 1, 50)
		if err != nil {
			return nil
		}
		return list
	})

	// 站点自定义路由（Go 层查数据, 模板纯展示）
	site.Get("/", func(ctx *web.CmsCtx) {
		latest, _, err := svc.List("article", core.StatusPublished, 1, 5)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}
		cats, err := catList(svc)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}
		ctx.Render([]string{"home.html"}, map[string]any{
			"Latest": latest, "Cats": cats,
		})
	})

	// 搜索页: /search?q=... （FTS5 + bigram）
	site.Get("/search", func(ctx *web.CmsCtx) {
		q := ctx.Query("q")
		ctx.Render([]string{"search.html"}, map[string]any{
			"Q": q,
			"Results": func() []core.Node {
				if q == "" {
					return nil
				}
				rows, _, err := svc.Search(q, "article", 1, 20)
				if err != nil {
					return nil
				}
				return rows
			}(),
		})
	})

	// 分类页: /category/{slug} → 子树内容列表
	site.Get("/category/{slug}", func(ctx *web.CmsCtx) {
		cat, err := svc.GetBySlug(ctx.PathValue("slug"))
		if err != nil || cat == nil {
			ctx.String(http.StatusNotFound, "404 page not found")
			return
		}
		ids, err := subtreeIDs(svc, cat.ID)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}
		ctx.Render(render.Candidates(cat), map[string]any{
			"Node": cat, "ID": cat.ID, "SubtreeIDs": ids,
		})
	})

	return nil
}

// ── seed 数据 ──────────────────────────────────────

func seed(svc *core.Service) error {
	// 分类树: 动态 > 时事资讯/智库活动; 研究 > 产业/区域
	news, err := svc.Create(&core.Node{Type: "category", Slug: "news",
		Status: core.StatusPublished, Sort: 1, Fields: core.Fields{"name": "动态"}})
	if err != nil {
		return err
	}
	current, err := svc.Create(&core.Node{Type: "category", Slug: "current",
		Status: core.StatusPublished, Sort: 1,
		Fields: core.Fields{"name": "时事资讯", "parent": news}})
	if err != nil {
		return err
	}
	research, err := svc.Create(&core.Node{Type: "category", Slug: "research",
		Status: core.StatusPublished, Sort: 2, Fields: core.Fields{"name": "研究"}})
	if err != nil {
		return err
	}
	industry, err := svc.Create(&core.Node{Type: "category", Slug: "industry",
		Status: core.StatusPublished, Sort: 1,
		Fields: core.Fields{"name": "产业研究", "parent": research}})
	if err != nil {
		return err
	}

	// 专家
	li, err := svc.Create(&core.Node{Type: "person", Slug: "li-zhiqi",
		Status: core.StatusPublished,
		Fields: core.Fields{"name": "李志起", "bio": "振兴国际智库理事长，长期跟踪研究高潜力企业群体。"}})
	if err != nil {
		return err
	}
	wang, err := svc.Create(&core.Node{Type: "person", Slug: "wang-x",
		Status: core.StatusPublished,
		Fields: core.Fields{"name": "王晓", "bio": "产业经济研究员，专注区域与产业规划。"}})
	if err != nil {
		return err
	}

	// 机构 + 任职（关系节点）
	viicn, err := svc.Create(&core.Node{Type: "org",
		Fields: core.Fields{"name": "振兴国际智库"}})
	if err != nil {
		return err
	}
	univ, err := svc.Create(&core.Node{Type: "org",
		Fields: core.Fields{"name": "某大学商学院"}})
	if err != nil {
		return err
	}
	// 任职: 李志起 → 智库理事长 / 大学客座
	if _, err := svc.Create(&core.Node{Type: "employment",
		Fields: core.Fields{"person": li, "org": viicn, "role": "理事长", "tenure": "2018-至今"}}); err != nil {
		return err
	}
	if _, err := svc.Create(&core.Node{Type: "employment",
		Fields: core.Fields{"person": li, "org": univ, "role": "客座教授", "tenure": "2020-2024"}}); err != nil {
		return err
	}

	// 文章（authors/categories 引用）
	articles := []struct {
		slug, title, body string
		authors           []any
		cats              []any
	}{
		{"ai-industry", "人工智能与制造业深度融合的路径", "<p>产业升级的下一站，是AI与制造的深度融合…</p>", []any{li, wang}, []any{industry, current}},
		{"region-plan", "区域协调发展中的产业选择", "<p>区域竞争的本质是产业赛道的选择…</p>", []any{wang}, []any{industry}},
		{"policy-2026", "2026 年经济政策解读", "<p>宏观政策的新取向与新抓手…</p>", []any{li}, []any{current}},
	}
	for i, a := range articles {
		if _, err := svc.Create(&core.Node{Type: "article", Slug: a.slug,
			Status: core.StatusPublished, Sort: i + 1,
			Fields: core.Fields{"title": a.title, "body": a.body, "authors": a.authors, "categories": a.cats}}); err != nil {
			return err
		}
	}
	return nil
}

// catList 顶级分类（首页导航）。
func catList(svc *core.Service) ([]core.Node, error) {
	// 顶级分类 = 无 parent 出边的 category（ListAny 返回 (list, total, err)）
	list, _, err := svc.ListAny("type = #{1} AND status = #{2}",
		[]any{"category", core.StatusPublished}, 1, 50)
	return list, err
}

// subtreeIDs 分类子树（含自身）。
func subtreeIDs(svc *core.Service, root int64) ([]int64, error) {
	ids, err := svc.Subtree(root, "parent", 20)
	if err != nil {
		return nil, err
	}
	return append([]int64{root}, ids...), nil
}

var _ = strconv.Itoa // 占位（后续分页用）
