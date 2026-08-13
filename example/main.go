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

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/core/render"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
	"github.com/kran/gcm/web"
	"github.com/kran/gcm/web/admin"
)

// adminPass 固定管理密码（测试方便）; 空 = 随机生成打印一次。
var adminPass = flag.String("admin-pass", "", "固定后台密码 (空 = 随机生成)")

func main() {
	flag.Parse()
	db, err := dba.Open("sqlite", "example.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Up(db); err != nil {
		log.Fatal(err)
	}

	ts := types.New()
	yaml, err := os.ReadFile("types.yaml")
	if err != nil {
		log.Fatal(err)
	}
	if err := ts.Load(yaml); err != nil {
		log.Fatal(err)
	}

	svc := core.New(db, ts)
	eng := render.New("templates", svc)
	site := web.New(svc, eng, "static")
	admin.Mount(site, svc, ts, "uploads")

	// 首次引导管理员账号（默认随机密码打印一次; -admin-pass 固定）
	if dc, err := admin.EnsureDefaults(db); err != nil {
		log.Fatal(err)
	} else if dc != nil {
		log.Printf("admin account created: %s / %s", dc.Username, dc.Password)
		if *adminPass != "" {
			if err := admin.NewService(db).SetPassword(*adminPass); err != nil {
				log.Fatal(err)
			}
			log.Printf("admin password set to fixed: %s", *adminPass)
		}
	}

	if err := seed(svc); err != nil {
		log.Fatal(err)
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
		renderHTML(ctx, site, eng, []string{"home.html"}, map[string]any{
			"Latest": latest, "Cats": cats,
		})
	})

	// 搜索页: /search?q=... （FTS5 + bigram）
	site.Get("/search", func(ctx *web.CmsCtx) {
		q := ctx.Query("q")
		renderHTML(ctx, site, eng, []string{"search.html"}, map[string]any{
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
		renderHTML(ctx, site, eng, render.Candidates(cat), map[string]any{
			"Node": cat, "ID": cat.ID, "SubtreeIDs": ids,
		})
	})

	log.Printf("example listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", site))
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

// renderHTML 站点级渲染出口（错误 → HTML 注释）。
func renderHTML(ctx *web.CmsCtx, site *web.Site, eng *render.Engine, candidates []string, data map[string]any) {
	// 复用 web 包的渲染出口不可行（未导出）— 站点项目自己处理
	// （此处直接调用引擎, 失败输出注释, 与 web 包行为一致）
	if err := eng.Render(ctx.W, candidates, data); err != nil {
		log.Printf("render failed: %s: %v", ctx.R.URL.Path, err)
		ctx.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = ctx.W.Write([]byte("<!-- render error: " + err.Error() + " -->"))
	}
}

var _ = strconv.Itoa // 占位（后续分页用）
