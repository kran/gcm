package core

// settings 站点配置（键值 + 运营元数据）。
//
// 配置类型注册表: 每项配置的类型 = 完整 FieldDef（kind + item/fields）,
// 站点 Go 代码声明（SettingTypes 注册）— settings 是类型系统的"应用",
// 校验直接复用 types.ValidateValue（复合递归 + 叶子 kind.Validate）。
// 表只存值 + 元数据; 定义在注册表（代码即配置, 重启不丢）。

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kran/gcm/types"
)

// SettingType 一项配置的类型定义（注册表条目）。
type SettingType struct {
	Key   string         // 配置 key
	Field types.FieldDef // 完整字段定义（kind + item/fields）— 校验与编辑的依据
	Note  string         // 描述（key 语义说明）
}

// Setting 一条站点配置（值 + 元数据; 类型在注册表）。
type Setting struct {
	Key       string    `db:"key" json:"key"`
	Kind      string    `db:"kind" json:"kind"` // 冗余副本（注册表权威; 表列保留便于追溯）
	Group     string    `db:"group_name" json:"group"`
	Note      string    `db:"note" json:"note"`
	RawValue  string    `db:"value" json:"-"`
	Value     any       `db:"-" json:"value"` // JSON 解码（按类型的形态）
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// SettingTypes 注册配置类型（站点 Setup 调用; 重复 key panic）。
// 注册表是 settings 的类型来源 — admin 按注册的 FieldDef 渲染编辑表单。
func (s *Service) SettingTypes(types ...SettingType) {
	for _, st := range types {
		if st.Key == "" || st.Field.Kind == "" {
			panic(fmt.Sprintf("core: setting type %q: key and kind required", st.Key))
		}
		if st.Field.Kind == "array" && st.Field.Item == nil {
			panic(fmt.Sprintf("core: setting type %q: array requires item", st.Key))
		}
		if _, dup := s.settingTypes[st.Key]; dup {
			panic(fmt.Sprintf("core: setting type %q already registered", st.Key))
		}
		s.settingTypes[st.Key] = st
	}
}

// SettingTypesView 注册表只读视图（admin 下发）。
func (s *Service) SettingTypesView() map[string]SettingType { return s.settingTypes }

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

// SetSetting upsert 一条配置的值: 类型取自注册表（SettingTypes 声明）,
// 值经 types.ValidateValue 校验（与节点字段同一套语义, 复合递归）。
// group/note 随注册表; 调用方无需传类型（类型是配置项的固有属性）。
func (s *Service) SetSetting(key string, value any) error {
	st, ok := s.settingTypes[key]
	if !ok {
		return fmt.Errorf("core: settings %q: not registered (SettingTypes first)", key)
	}
	if err := s.types.ValidateValue("settings."+key, st.Field, value); err != nil {
		return fmt.Errorf("core: settings %q: %w", key, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("core: settings %q: marshal: %w", key, err)
	}
	if _, err := s.db.Add(
		`INSERT INTO settings (key, group_name, note, value, updated_at)
		 VALUES (#{1}, #{2}, #{3}, #{4}, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET group_name = #{2}, note = #{3}, value = #{4}, updated_at = datetime('now')`,
		key, st.Field.Kind, st.Note, string(raw)).Exec(); err != nil {
		return fmt.Errorf("core: settings %q: %w", key, err)
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
