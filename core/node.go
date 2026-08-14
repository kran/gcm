// Package core 核心引擎: 节点 + 引用（设计文档 §7）。
//
// 节点与引用是一个引擎的两面: 节点的 ref 字段值落 edges 表,
// fields 只存标量字段（引用是共享数据, 不归节点私有）。
// 引擎不认识业务, 只认识类型定义（types 组件）与节点/引用。
package core

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Fields 节点标量字段（ref 字段不在其中 — 引用在 edges 表）。
type Fields map[string]any

// Value driver.Valuer: 存库为 JSON。
func (f Fields) Value() (driver.Value, error) {
	if f == nil {
		return "{}", nil
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("core: fields marshal: %w", err)
	}
	return string(b), nil
}

// Scan sql.Scanner: 读库解 JSON。
func (f *Fields) Scan(v any) error {
	if v == nil {
		*f = Fields{}
		return nil
	}
	b, ok := v.([]byte)
	if !ok {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("core: fields scan: unexpected type %T", v)
		}
		b = []byte(s)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("core: fields scan: %w", err)
	}
	*f = m
	return nil
}

// Node 节点: 一切实体（内容 / term / 关系节点）, type 区分。
// URL 默认节点 URL（gcm 路由约定 /node/{slug|id}）; nil 安全返回 "#"。
// URL 模式是站点渲染层的事 — 这是 gcm 默认路由的约定值, 站点自定义
// 用 HookNodeEnrich 覆盖 Extra["url"]。
func (n *Node) URL() string {
	if n == nil {
		return "#"
	}
	if n.Slug != "" {
		return "/node/" + n.Slug
	}
	return "/node/" + strconv.FormatInt(n.ID, 10)
}

type Node struct {
	ID        int64     `db:"id,omitempty" json:"id"` // omitempty: 插入时跳零值走自增
	Type      string    `db:"type" json:"type"`
	Title     string    `db:"title" json:"title"` // 显示名投影列（类型 title 声明映射）
	Slug      string    `db:"slug" json:"slug"`   // '' = 无 URL 段
	Status    int       `db:"status" json:"status"`
	Sort      int       `db:"sort" json:"sort"`
	Fields    Fields    `db:"fields" json:"fields"` // 标量字段（ref 字段在 edges, 不含于此）
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
	// Expand 路径展开容器（ExpandPath 填充）: map[字段名] → *Node（ref 单值）
	// 或 []*Node（ref[] 数组）— 形态由类型定义（Class）驱动。常规查询为空。
	// 递归层级经路径表达式表达（"a.b"）: 子节点的 Expand 再挂下一层, 树形
	// 结构天然可遍历 — 不再需要独立的 NodesOut/NodesIn 字段。
	Expand map[string]any `db:"-" json:"expand,omitempty"`
	// Extra 渲染期附加数据（站点 NodeData hook / 渲染层填充; 不落库）:
	// URL 生成、高亮标记、面包屑等 — 模板统一 .Node.Extra.x 访问。
	Extra map[string]any `db:"-" json:"extra,omitempty"`
}
