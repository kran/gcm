package core

// settings 站点配置（cmx piece 语义移植 + group）:
//
//	Key   — 唯一标识（footer / seo.default）
//	Group — 运营分组（site / seo / list; 后台按组分栏, 未来权限挂载点）
//	Type  — 编辑形态（string/text/number/bool/object/array/file/richtext）
//	        前端按形态渲染控件; 与类型系统的 kind 无关（运营配置是自由 JSON,
//	        受信写者, 零后端校验 — types 的强校验留给内容结构）
//	Value — 自由 JSON 文本（object = 键值对, array = 元素列表 — 前端自由编辑）
//
// 硬性边界（cmx 同款）: key 唯一、无列表语义、不作查询条件 — 需要按字段
// 筛选或多条排序的数据必须成为 nodes 的一个 type。

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Setting 一条站点配置。
type Setting struct {
	Key       string    `db:"key" json:"key"`
	Group     string    `db:"group_name" json:"group"`
	Type      string    `db:"type" json:"type"` // 编辑形态
	RawValue  string    `db:"value" json:"-"`
	Value     any       `db:"-" json:"value"` // JSON 解码
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

func checkKey(key string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("settings: key %q must match %s", key, keyRe)
	}
	return nil
}

// Get 取一条; 未找到返回 (nil, nil)。
func (s *Service) GetSetting(key string) (*Setting, error) {
	var st Setting
	found, err := s.db.Add(`SELECT * FROM settings WHERE key = #{1}`, key).Get(&st)
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	if !found {
		return nil, nil
	}
	if err := st.decode(); err != nil {
		return nil, fmt.Errorf("settings: %s value is invalid JSON: %w", key, err)
	}
	return &st, nil
}

// GetSettingValue 取一条并把 value JSON 解进 dest; 未找到返回 (false, nil)。
func (s *Service) GetSettingValue(key string, dest any) (bool, error) {
	st, err := s.GetSetting(key)
	if err != nil || st == nil {
		return false, err
	}
	raw := []byte(st.RawValue)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, fmt.Errorf("settings: %s value invalid: %w", key, err)
	}
	return true, nil
}

// GetSettings 批量取（键列表, 缺失键静默跳过 — 模板渲染一次拿全站配置）。
func (s *Service) GetSettings(keys []string) (map[string]Setting, error) {
	if len(keys) == 0 {
		return map[string]Setting{}, nil
	}
	var rows []Setting
	if err := s.db.Add(`SELECT * FROM settings WHERE key IN (#{1|expand})`, keys).List(&rows); err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	out := map[string]Setting{}
	for _, r := range rows {
		if err := r.decode(); err != nil {
			return nil, fmt.Errorf("settings: %q: %w", r.Key, err)
		}
		out[r.Key] = r
	}
	return out, nil
}

// ListSettings 全部配置（可按分组过滤; 空组 = 全部）。
func (s *Service) ListSettings(group string) ([]Setting, error) {
	q := s.db.Add(`SELECT * FROM settings`)
	if group != "" {
		q = q.Add(`WHERE group_name = #{1}`, group)
	}
	q = q.Add(`ORDER BY group_name, key`)
	var rows []Setting
	if err := q.List(&rows); err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	for i := range rows {
		if err := rows[i].decode(); err != nil {
			return nil, fmt.Errorf("settings: %q: %w", rows[i].Key, err)
		}
	}
	return rows, nil
}

// Set upsert: type 记录编辑形态, value 序列化为 JSON 落库（零校验, 透传 —
// 运营配置受信写者; 语义归 UI/模板）。
func (s *Service) SetSetting(key, group, typ string, value any) error {
	if err := checkKey(key); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("settings: %s value not JSON-serializable: %w", key, err)
	}
	if _, err := s.db.Add(
		`INSERT INTO settings (key, group_name, type, value, updated_at)
		 VALUES (#{1}, #{2}, #{3}, #{4}, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET group_name = #{2}, type = #{3}, value = #{4}, updated_at = datetime('now')`,
		key, group, typ, string(data)).Exec(); err != nil {
		return fmt.Errorf("settings: %s: %w", key, err)
	}
	return nil
}

// Delete 删除一条; 不存在报错。
func (s *Service) DeleteSetting(key string) error {
	affected, err := s.db.Add(`DELETE FROM settings WHERE key = #{1}`, key).Exec()
	if err != nil {
		return err
	}
	n, _ := affected.RowsAffected()
	if n == 0 {
		return errors.New("settings: not found: " + key)
	}
	return nil
}

// decode value JSON → Value。
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
