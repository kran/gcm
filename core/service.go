package core

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcm/core/hook"
	"github.com/kran/gcm/types"
	_ "modernc.org/sqlite" // sqlite driver 注册
)

// ErrNotFound 目标节点不存在。
var ErrNotFound = errors.New("core: node not found")

// 发布状态。
const (
	StatusDraft     = 0
	StatusPublished = 1
)

// Service 核心引擎 — 每站点一个实例, 绑定本站 db + 本站类型系统。
// 节点 CRUD 与引用落边是一个事务（ref 字段值进 edges, fields 只存标量）。
// 标准 hook 事件（扩展挂载点; 通用组件不依赖业务语义）:
//
//	HookNodeSave  func(*Node) error   — CreateNode/UpdateNode 事务内（失败回滚）
//	HookNodeDelete func(int64) error  — DeleteNode 事务内
//
// 扩展: svc.Hooks().AddHook(core.HookNodeSave, fn, priority)
const (
	HookNodeSave   = "node.save"
	HookNodeDelete = "node.delete"
)

type Service struct {
	db          *dba.SQL
	dao         *dba.Dao[Node]
	types       *types.Types
	search      SearchIndex // 全文检索引擎（默认 FTS5+bigram; SetSearchIndex 可换）
	hooks       *hook.Bus
	filterCache sync.Map             // filter 表达式 → *CompiledFilter（编译缓存, 消费方共享）
	lispFuncsC  map[string]LispFuncC // Lisp filter 函数注册表（站点扩展; 内置在编译器）
}

// Hooks 站点级 hook 总线（注册扩展; 执行顺序 = priority 升序 + 注册序稳定）。
func (s *Service) Hooks() *hook.Bus { return s.hooks }

// Types 类型系统容器（站点注册自定义 kind）。
func (s *Service) Types() *types.Types { return s.types }

// New 建引擎。
func New(db *dba.SQL, ts *types.Types) *Service {
	svc := &Service{db: db, dao: dba.NewDao[Node](db, "nodes"), types: ts, lispFuncsC: map[string]LispFuncC{}}
	svc.search = NewFTSIndex(svc)
	// 标准事件声明（注册即校验签名）
	svc.hooks = hook.New()
	if err := svc.hooks.Define(
		hook.Spec{Name: HookNodeSave, Proto: func(*Node) error { return nil }},
		hook.Spec{Name: HookNodeDelete, Proto: func(int64) error { return nil }},
	); err != nil {
		panic("core: define standard hooks: " + err.Error())
	}
	return svc
}

// DB 暴露本站 db（管理通道/业务表用）。
// DB 底层数据库句柄（逃生舱）: 复杂聚合/迁移脚本用裸 SQL。
// ⚠ 绕过类型系统: 引用语义（edges）、title 投影、字段 JSON 需自行维护;
// 常规路径请用 Service 方法或 render 查询函数。
func (s *Service) DB() *dba.SQL { return s.db }

// ── 写 ─────────────────────────────────────────

// CreateNode 建节点: 校验字段 → 抽取 ref 字段值落 edges → 存节点。
// fields 中的 ref 字段值是 id（ref）或 id 数组（ref[]）;
// 落库后 fields 只含标量字段。
func (s *Service) CreateNode(n *Node) (int64, error) {
	if n == nil {
		return 0, errors.New("core: create: nil node")
	}
	td, ok := s.types.Type(n.Type)
	if !ok {
		return 0, fmt.Errorf("core: type %q not defined", n.Type)
	}
	if err := s.types.ValidateFields(n.Type, n.Fields); err != nil {
		return 0, err
	}
	if n.Slug != "" && !types.ValidSlug(n.Slug) {
		return 0, fmt.Errorf("core: invalid slug %q (must start with letter, only letters/digits/_/-, no consecutive --)", n.Slug)
	}
	n.Title = s.titleFrom(td, n.Fields)
	scalar, refs, err := s.splitFields(td, n.Fields)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		dao := s.dao.WithTx(tx)
		n.Fields = scalar
		n.CreatedAt = time.Now()
		n.UpdatedAt = time.Now()
		var err error
		id, err = dao.Create(n)
		if err != nil {
			return err
		}
		if err := s.addEdges(tx, id, td, refs); err != nil {
			return err
		}
		// 搜索同步 + 扩展钩子（事务内, 失败回滚 = fail-loud）
		n.ID = id
		return s.syncSearchAndFire(tx, n)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateNode 全量更新（无差量语义）: 校验 → 事务（替换该类型全部 ref 字段的
// 边 + 更新节点列）。
// ⚠ 调用约定: n 是**完整状态** — 从零构造 Node 只改部分字段会清空
// 未赋值的 Slug/Status/Sort。安全模式: GetNodeById(id) → 改字段 → UpdateNode。
// n.Type 不可变（防改类型破坏引用语义）。n 会被就地修改（Fields 剥掉
// ref 字段, UpdatedAt 刷新）。
func (s *Service) UpdateNode(n *Node) error {
	if n == nil {
		return errors.New("core: update: nil node")
	}
	existing, err := s.GetNodeById(n.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}
	if n.Type != existing.Type {
		return fmt.Errorf("core: update: type %q != existing %q (type is immutable)", n.Type, existing.Type)
	}
	td, ok := s.types.Type(existing.Type)
	if !ok {
		return fmt.Errorf("core: type %q not defined", existing.Type)
	}
	if err := s.types.ValidateFields(existing.Type, n.Fields); err != nil {
		return err
	}
	if n.Slug != "" && !types.ValidSlug(n.Slug) {
		return fmt.Errorf("core: invalid slug %q (must start with letter, only letters/digits/_/-, no consecutive --)", n.Slug)
	}
	// 内部拷贝: 不触碰调用方传入的 Node（无隐蔽副作用）。
	m := *n
	m.Title = s.titleFrom(td, n.Fields)
	scalar, refs, err := s.splitFields(td, n.Fields)
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *dba.SQL) error {
		// 替换该类型全部 ref 字段的出边
		for _, f := range td.Fields {
			if s.types.IsRefKind(f.Kind) {
				if _, err := tx.Add(
					`DELETE FROM edges WHERE from_node = #{1} AND field = #{2}`, n.ID, f.Name).Exec(); err != nil {
					return err
				}
			}
		}
		dao := s.dao.WithTx(tx)
		m.Fields = scalar
		m.UpdatedAt = time.Now()
		if _, err := dao.Update(&m, "id = #{1}", n.ID); err != nil {
			return err
		}
		if err := s.addEdges(tx, n.ID, td, refs); err != nil {
			return err
		}
		m.ID = n.ID
		// 搜索同步 + 扩展钩子（事务内, 失败回滚）
		return s.syncSearchAndFire(tx, &m)
	})
}

// DeleteNode 删节点: 显式清全部出/入引用 + 删节点（事务, 不依赖 PRAGMA FK）。
func (s *Service) DeleteNode(id int64) error {
	return s.db.Transaction(func(tx *dba.SQL) error {
		if _, err := tx.Add(
			`DELETE FROM edges WHERE from_node = #{1} OR to_node = #{1}`, id).Exec(); err != nil {
			return err
		}
		affected, err := s.dao.WithTx(tx).Delete("id = #{1}", id)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		if err := s.search.Delete(tx, id); err != nil {
			return err
		}
		// 扩展钩子（事务内, 失败回滚）
		return s.hooks.Fire(HookNodeDelete, id)
	})
}

// syncSearchAndFire 写路径公共尾部: 全文索引同步（search:true + 已发布才进,
// 业务规则在 Service）+ 扩展钩子。事务内执行, 失败回滚（fail-loud）。
func (s *Service) syncSearchAndFire(tx *dba.SQL, n *Node) error {
	if s.searchableType(n.Type) && n.Status == StatusPublished {
		if err := s.search.Sync(tx, n); err != nil {
			return err
		}
	} else if err := s.search.Delete(tx, n.ID); err != nil {
		return err
	}
	return s.hooks.Fire(HookNodeSave, n)
}

// ── 读 ─────────────────────────────────────────

// ListAny 通用分页列表（管理通道: where 自定义, 占位符从 #{1} 起）。
func (s *Service) ListAny(where string, args []any, page, size int) ([]Node, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	return s.dao.Page(page, size, where+" ORDER BY id DESC", args...)
}

// GetNodeById 按 id 取节点; 不存在返回 (nil, nil) — 查询语义（"找不到"不是错误,
// 模板层 get 返回 nil 渲染空）。管理 API 需区分时用 FullFields（ErrNotFound）。
func (s *Service) GetNodeById(id int64) (*Node, error) {
	return s.dao.GetByID(id)
}

// FullFields 管理视图: 节点 fields + ref 字段值（id 列表）— 编辑表单回显用。
// 存储的 fields 不含 ref（引用在 edges）; 此方法把它们组装回来。
// 模板层不用（模板用 outRefs/inRefs 取引用, 保持投影纪律）。
func (s *Service) FullFields(id int64) (Fields, error) {
	n, err := s.GetNodeById(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNotFound
	}
	td, ok := s.types.Type(n.Type)
	if !ok {
		return nil, fmt.Errorf("core: type %q not defined", n.Type)
	}
	out := Fields{}
	for k, v := range n.Fields {
		out[k] = v
	}
	for _, f := range td.Fields {
		if !s.types.IsRefKind(f.Kind) {
			continue
		}
		ids, err := s.refTargetIDs(id, f.Name)
		if err != nil {
			return nil, err
		}
		k, ok := s.types.Kind(f.Kind)
		if !ok {
			continue
		}
		if k.Class() == types.ClassRef {
			// ref 单个: 取第一条
			if len(ids) > 0 {
				out[f.Name] = ids[0]
			}
		} else {
			anyIDs := make([]any, 0, len(ids))
			for _, tid := range ids {
				anyIDs = append(anyIDs, tid)
			}
			out[f.Name] = anyIDs
		}
	}
	return out, nil
}

// refTargetIDs 某节点某引用字段的全部目标 id（管理回显; 一条 SQL 全量,
// 无分页上限 — 修复原先 OutEdges(...,1,1000) 的静默截断）。
func (s *Service) refTargetIDs(id int64, field string) ([]int64, error) {
	var ids []int64
	q := s.db.Add(`SELECT to_node FROM edges WHERE from_node = #{1} AND field = #{2} ORDER BY sort, id`,
		id, field)
	if err := q.List(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// GetNodeBySlug 按 slug 取节点; 不存在返回 (nil, nil)。
// 空 slug 永不命中（部分唯一索引: 空 slug 不进索引）。
func (s *Service) GetNodeBySlug(slug string) (*Node, error) {
	if slug == "" {
		return nil, nil
	}
	return s.dao.Get("slug = #{1}", slug)
}

// List 按类型 + 状态分页列表（status<0 不过滤）。
// ORDER BY sort, id DESC。
func (s *Service) List(typeName string, status int, page, size int) ([]Node, int64, error) {
	where := "type = #{1}"
	args := []any{typeName}
	if status >= 0 {
		where += " AND status = #{2}"
		args = append(args, status)
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	total, err := s.dao.Count(where, args...)
	if err != nil {
		return nil, 0, err
	}
	list, _, err := s.dao.Page(page, size, where+" ORDER BY sort, id DESC", args...)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// titleFrom 抽标题列: 类型 title 声明字段的值（双存: fields 保留完整,
// 列是投影 — 单事务内同步, 无不一致窗口）。无声明 → 空。
// 支持两种声明: "字段名"（本类型标量字段）和穿透 "ref.$.字段"/"ref.列"
// （引用目标的字段/列 — 关系节点标题, 如 employment: person.$.name）。
// ⚠ 穿透是写时快照: 目标节点改字段后, 引用方 title 列不自动刷新
// （需求信号出现再做级联; 列表已默认 expand, 前端可显示实时值）。
func (s *Service) titleFrom(td types.TypeDef, fields Fields) string {
	if td.Title == "" {
		return ""
	}
	path, err := types.ParsePath(td.Title)
	if err != nil || len(path) == 0 {
		return "" // 声明非法由 types 校验期拒绝; 此处防御
	}
	if len(path) == 1 {
		// 本类型标量字段
		if v, ok := fields[path[0].Field].(string); ok {
			return v
		}
		return ""
	}
	// 穿透（两段）: ref 字段值（调用方 fields 含 ref 值; ref[] 取第一条）
	v, ok := fields[path[0].Field]
	if !ok {
		return ""
	}
	var tid int64
	switch n := v.(type) {
	case int64:
		tid = n
	case float64:
		tid = int64(n)
	case []any:
		if len(n) > 0 {
			tid, _ = ToID(n[0])
		}
	}
	if tid <= 0 {
		return ""
	}
	target, err := s.GetNodeById(tid)
	if err != nil || target == nil {
		return "" // 引用目标缺失由 addEdges 的 checkTarget 报（主错误响亮）
	}
	seg2 := path[1]
	if seg2.JSON {
		if tv, ok := target.Fields[seg2.Field].(string); ok {
			return tv
		}
		return ""
	}
	switch seg2.Field {
	case "title":
		return target.Title
	case "slug":
		return target.Slug
	case "status":
		return strconv.Itoa(target.Status)
	case "sort":
		return strconv.Itoa(target.Sort)
	}
	return ""
}

// ── 引用落边（引擎内部）────────────────────────

// addEdges 校验 ref 目标（存在 + 类型匹配）并插入边。
func (s *Service) addEdges(tx *dba.SQL, from int64, td types.TypeDef, refs map[string]any) error {
	for fieldName, v := range refs {
		f, ok := types.FieldByName(td, fieldName)
		if !ok {
			return fmt.Errorf("core: field %q not on type %q", fieldName, td.Name)
		}
		ids, err := s.refIDs(f, v)
		if err != nil {
			return err
		}
		for _, to := range ids {
			if err := s.checkTarget(tx, to, f.To); err != nil {
				return fmt.Errorf("core: %q.%s -> %d: %w", td.Name, fieldName, to, err)
			}
			if _, err := tx.Add(
				`INSERT INTO edges (from_node, field, to_node, sort, created_at)
				 VALUES (#{1}, #{2}, #{3}, #{4}, datetime('now'))`,
				from, fieldName, to, 0).Exec(); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkTarget 目标存在且类型匹配。
func (s *Service) checkTarget(tx *dba.SQL, id int64, wantType string) error {
	var typ string
	found, err := tx.Add(`SELECT type FROM nodes WHERE id = #{1}`, id).Get(&typ)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("target %d not found", id)
	}
	if typ != wantType {
		return fmt.Errorf("target %d is type %q, want %q", id, typ, wantType)
	}
	return nil
}

// ── 工具 ─────────────────────────────────────────

// splitFields 把 fields 分成标量（落节点）与引用（落 edges）——
// 引用判断走类型容器的存储形态（kind 自己说了算, 不是按 To 猜）。
func (s *Service) splitFields(td types.TypeDef, fields Fields) (Fields, map[string]any, error) {
	scalar := Fields{}
	refs := map[string]any{}
	for name, v := range fields {
		f, ok := types.FieldByName(td, name)
		if !ok {
			return nil, nil, fmt.Errorf("core: field %q not on type %q", name, td.Name)
		}
		if s.types.IsRefKind(f.Kind) {
			refs[name] = v
		} else {
			scalar[name] = v
		}
	}
	return scalar, refs, nil
}

// refIDs 引用字段值 → id 列表（ref 单个包一层, ref[] 原样）。
func (s *Service) refIDs(f types.FieldDef, v any) ([]int64, error) {
	k, ok := s.types.Kind(f.Kind)
	if !ok {
		return nil, fmt.Errorf("core: unknown kind %q", f.Kind)
	}
	switch k.Class() {
	case types.ClassRef:
		id, err := types.ToID(v)
		if err != nil {
			return nil, fmt.Errorf("core: ref value: %w", err)
		}
		return []int64{id}, nil
	case types.ClassRefList:
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("core: ref[] value: expects array, got %T", v)
		}
		ids := make([]int64, 0, len(arr))
		for i, e := range arr {
			id, err := types.ToID(e)
			if err != nil {
				return nil, fmt.Errorf("core: ref[] value[%d]: %w", i, err)
			}
			ids = append(ids, id)
		}
		return ids, nil
	default:
		return nil, fmt.Errorf("core: %s is not a ref kind", f.Kind)
	}
}

// ToID 数值 → int64（JSON 解码后是 float64）。
func ToID(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("expects integer node id, got %v", n)
		}
		return int64(n), nil
	default:
		return 0, fmt.Errorf("expects node id (int64), got %T", v)
	}
}

// ── 结构化查询（base SQL 模板 + var 槽）─────────────
//
// ListQuery 是 List/ListFiltered 的结构化替代: 过滤（Lisp 表达式）、排序、
// 展开、分页 — 内部用 dba var 槽（${where} ${order}）组合, 不拼字符串。
// Lisp filter 编译器（CompileLispInto）作为 ${where} 槽的挂载器。
type ListQuery struct {
	Type   string // 类型过滤（合成 (= type "x") 条件; 空 = 不过滤类型）
	Host   string // 编译宿主（Lisp 字段校验依据; 空 = Type）
	Filter string // Lisp filter 表达式（空 = 不过滤）
	Sort   string // 排序（空 = 默认 ORDER BY sort, id DESC）
	Expand string // 展开表达式（预留）
	Page   int
	Size   int
}

// ListQWithParams ListQ + 占位符参数绑定（filter 里的 {:name}）。
func (s *Service) ListQWithParams(q ListQuery, params map[string]any) ([]Node, int64, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 {
		q.Size = 20
	}
	// type 条件合成进 filter（列比较, 参数化）: (and (= type "xxx") <user filter>)
	// — ListQ.Type 的语义 = 类型过滤; Host = 编译宿主（字段校验; 空 = Type）
	whereExpr := q.Filter
	if q.Type != "" {
		if whereExpr != "" {
			whereExpr = `(and (= type "` + q.Type + `") ` + whereExpr + `)`
		} else {
			whereExpr = `(= type "` + q.Type + `")`
		}
	}
	host := q.Host
	if host == "" {
		host = q.Type
	}
	// dba.Page 协议: ${F:*}（列槽, count 时换 COUNT(1)）+ ${order:}（排序槽,
	// count 自动清空）— count/data 同 base 不可变分叉, filter 只编译一次。
	db := s.db.Add(`SELECT ${F:*} FROM nodes WHERE ${where} ${order:ORDER BY sort, id DESC}`)
	if whereExpr != "" {
		var err error
		db, err = s.CompileLispInto(db, whereExpr, host, params)
		if err != nil {
			return nil, 0, err
		}
	} else {
		db = db.Var("where", "1 = 1")
	}
	if q.Sort != "" {
		db = db.Var("order", "ORDER BY "+q.Sort)
	}
	rows, total, err := dba.Page[Node](db, q.Page, q.Size)
	if err != nil {
		return nil, 0, err
	}
	// Expand 接线: 批量路径展开（查询次数 = 路径长度, 与列表大小无关）
	if q.Expand != "" {
		ids := make([]int64, len(rows))
		for i := range rows {
			ids[i] = rows[i].ID
		}
		expanded, err := s.ExpandPathMany(ids, q.Expand)
		if err != nil {
			return nil, 0, err
		}
		for i := range rows {
			rows[i].Expand = expanded[i].Expand
		}
	}
	return rows, total, nil
}

// ListQ 结构化查询执行: base SQL 模板 + ${where}/${order} var 槽。
func (s *Service) ListQ(q ListQuery) ([]Node, int64, error) {
	return s.ListQWithParams(q, nil)
}
