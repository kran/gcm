package core

import "fmt"

// ── 代数原语 ─────────────────────────────────────────
//
// symmetric 已在 M3（OutRefs/InRefs 双向展开）; 反向查询无需声明（边双向）。
// 这里补 transitive（可达性）与 equivalence（等价类）。
// 原语是通用能力（任何 ref 字段都能递归）; 代数声明决定查询语言
// （M5）里是否允许闭包/等价展开 — 原语本身不检查代数。

// Traverse 沿 field 出边递归（向上: 祖先链）。maxHops 上限防环（有界原则）。
// 返回可达节点 id（含多跳, 不含起点）。
func (s *Service) Traverse(start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT to_node, 1 FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, w.depth + 1 FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// Subtree 沿 field 入边递归（向下: 子树/后代）。maxHops 上限防环。
// 返回全部后代节点 id（不含起点）。
func (s *Service) Subtree(start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT from_node, 1 FROM edges WHERE field = #{1} AND to_node = #{2}
			UNION ALL
			SELECT e.from_node, w.depth + 1 FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// EquivalenceClass 等价类展开: 沿 field 出边+入边双向递归
// （等价关系无方向 — 即使只存单向边, 类内全部成员可达）。
// 返回含起点在内的整个等价类。maxHops 上限（等价类应小, 防御环）。
func (s *Service) EquivalenceClass(start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT #{2}, 0
			UNION
			SELECT e.to_node, w.depth + 1 FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
			UNION
			SELECT e.from_node, w.depth + 1 FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// walk 执行递归 CTE, 返回 id 列表。
func (s *Service) walk(cte string, args ...any) ([]int64, error) {
	var ids []int64
	if err := s.db.Add(cte, args...).List(&ids); err != nil {
		return nil, fmt.Errorf("core: traverse: %w", err)
	}
	return ids, nil
}
