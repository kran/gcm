package web

// PageData 页面上下文（所有页面渲染统一注入 — HookRender 默认实现）:
//
//	Path         — 当前请求路径
//	Ancestors    — 祖先链（根→叶, 含当前分类; 面包屑/多级菜单用）
//	TopCategory  — 当前分类（分类页 = 自身; 内容页 = 所属分类）
//	HasActive    — 导航高亮（子树语义: 目标分类是当前链祖先即亮）
//	QueryInt     — 请求参数（分页等）
//
// 分类链按约定字段名计算（Site.CategoryField/ContentCategoryField,
// 默认 "parent"/"categories"）; 站点字段名不同改 Site 配置即可。

import (
	"github.com/kran/gcm/core"
)

// PageData 页面上下文。
type PageData struct {
	Path        string
	Ancestors   []*core.Node
	TopCategory *core.Node
	chainSlugs  map[string]bool
	ctx         *CmsCtx
}

// HasActive 导航高亮: 子树语义 — 目标分类 slug 是当前链祖先（含自身）即亮。
func (p *PageData) HasActive(slug string) bool {
	if slug == "" {
		return false
	}
	if p.chainSlugs == nil {
		return p.Path == slug
	}
	return p.chainSlugs[slug]
}

// QueryInt 请求参数。
func (p *PageData) QueryInt(name string) int {
	if p.ctx == nil {
		return 0
	}
	return p.ctx.QueryInt(name, 0)
}

// pageData 构造页面上下文（约定字段: parent = 分类父引用, categories = 内容分类挂载）。
func (s *Site) pageData(ctx *CmsCtx, node *core.Node) *PageData {
	pd := &PageData{Path: ctx.R.URL.Path, ctx: ctx, chainSlugs: map[string]bool{}}
	if node == nil {
		return pd
	}
	var chainRoot *core.Node
	if node.Type == s.categoryType {
		chainRoot = node
	} else {
		cats, _, err := s.svc.OutRefs(node.ID, s.contentCategoryField, 1, 5)
		if err == nil && len(cats) > 0 {
			chainRoot, _ = s.svc.Get(cats[0].ToNode)
		}
	}
	if chainRoot != nil {
		pd.TopCategory = chainRoot
		pd.chainSlugs[chainRoot.Slug] = true
		chain, _ := s.svc.Traverse(chainRoot.ID, s.categoryField, 20)
		// 根→叶: traverse 是叶→根（[父, 祖父...]）— 反转后自身在末尾
		for i := len(chain) - 1; i >= 0; i-- {
			if anc, _ := s.svc.Get(chain[i]); anc != nil {
				pd.Ancestors = append(pd.Ancestors, anc)
				pd.chainSlugs[anc.Slug] = true
			}
		}
		pd.Ancestors = append(pd.Ancestors, chainRoot)
	}
	return pd
}
