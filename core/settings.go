package core

// settings 站点配置（键值 + 运营元数据）。
//
// 值按 kind 的形态存 JSON（kind = 类型系统的 kind 名, 校验/编辑形态复用）;
// group_name 分组（运营分类, 未来权限挂载点）; note 描述（key 语义说明）。

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kran/gcm/types"
)

// Setting 一条站点配置。
type Setting struct {
	Key       string    `db:"key" json:"key"`
	Group     string    `db:"group_name" json:"group"`
	Kind      string    `db:"kind" json:"kind"`
	Note      string    `db:"note" json:"note"`
	RawValue  string    `db:"value" json:"-"`
	Value     any       `db:"-" json:"value"` // JSON 解码（按 kind 的形态）
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// GetSetting 取一条; 缺失返回 (nil, nil)。
func (s *Service) GetSetting(key string) (*Setting, error) {
	var st Setting
	found, err := s.db.Add(`SELECT * FROM settings WHERE key = #{1}`, key).Get(&st)
	if err != nil {
		return nil, fmt.Errorf("core: settings: %w", err)
	}
	if !found {
		return nil, nil
	}
	if err := st.decode(); err != nil {
		return nil, fmt.Errorf("core: settings %q: %w", key, err)
	}
	return &st, nil
}

// GetSettings 批量取（键列表, 缺失键静默跳过 — 模板渲染一次拿全站配置）。
func (s *Service) GetSettings(keys []string) (map[string]Setting, error) {
	if len(keys) == 0 {
		return map[string]Setting{}, nil
	}
	var rows []Setting
	if err := s.db.Add(`SELECT * FROM settings WHERE key IN (#{1|expand})`, keys).List(&rows); err != nil {
		return nil, fmt.Errorf("core: settings: %w", err)
	}
	out := map[string]Setting{}
	for _, r := range rows {
		if err := r.decode(); err != nil {
			return nil, fmt.Errorf("core: settings %q: %w", r.Key, err)
		}
		out[r.Key] = r
	}
	return out, nil
}

// ListSettings 全部设置（可按分组过滤; 空组 = 全部）。
func (s *Service) ListSettings(group string) ([]Setting, error) {
	q := s.db.Add(`SELECT * FROM settings`)
	if group != "" {
		q = q.Add(`WHERE group_name = #{1}`, group)
	}
	q = q.Add(`ORDER BY group_name, key`)
	var rows []Setting
	if err := q.List(&rows); err != nil {
		return nil, fmt.Errorf("core: settings: %w", err)
	}
	for i := range rows {
		if err := rows[i].decode(); err != nil {
			return nil, fmt.Errorf("core: settings %q: %w", rows[i].Key, err)
		}
	}
	return rows, nil
}

// SetSetting upsert 一条配置: kind 必须已注册（复用类型系统校验）+ 值经
// kind.Validate（fail-loud）。group/note 是元数据透传。
func (s *Service) SetSetting(st Setting) error {
	// kind 配置实例语义: settings 每一项 = 一个 kind 被应用（携带完整语义
	// 校验 + 编辑形态）。复合 kind 用默认配置 — array = array<string>
	// （标签/列表）; object = 自由 map（形状检查, 子定义等 schema 注册机制）。
	// 值经 ValidateValue 校验（叶子走 kind.Validate, 复合递归）。
	fd := types.FieldDef{Kind: st.Kind}
	switch st.Kind {
	case "array":
		fd.Item = &types.FieldDef{Kind: "string"}
	case "object":
		// 自由 map: 仅形状检查（无子定义）
		if _, ok := st.Value.(map[string]any); !ok {
			return fmt.Errorf("core: settings %q: object expects map[string]any, got %T", st.Key, st.Value)
		}
		return nil
	}
	if err := s.types.ValidateValue("settings", fd, st.Value); err != nil {
		return fmt.Errorf("core: settings %q: %w", st.Key, err)
	}
	raw, err := json.Marshal(st.Value)
	if err != nil {
		return fmt.Errorf("core: settings %q: marshal: %w", st.Key, err)
	}
	if _, err := s.db.Add(
		`INSERT INTO settings (key, group_name, kind, note, value, updated_at)
		 VALUES (#{1}, #{2}, #{3}, #{4}, #{5}, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET group_name = #{2}, kind = #{3}, note = #{4}, value = #{5}, updated_at = datetime('now')`,
		st.Key, st.Group, st.Kind, st.Note, string(raw)).Exec(); err != nil {
		return fmt.Errorf("core: settings %q: %w", st.Key, err)
	}
	return nil
}

// decode value JSON → Value（按存储原样解码, 校验已由写入路径保证）。
func (st *Setting) decode() error {
	if st.RawValue == "" || st.RawValue == "{}" {
		st.Value = nil
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(st.RawValue), &v); err != nil {
		return err
	}
	st.Value = v
	return nil
}
