// Package admin 管理后端: 登录（accounts + bcrypt + session cookie）+
// 按 type 分组的实体管理 API + 引用编辑（ref 字段经 fields 提交, 引擎落边）。
// 单账号每库（accounts 表, 每站一库无 site 列）。
package admin

import (
	"crypto/rand"
	"embed"
	"errors"
	"io/fs"
	"math/big"
	"strings"
	"time"

	"github.com/kran/dba"
	"golang.org/x/crypto/bcrypt"
)

//go:embed web
var webFS embed.FS

// uiFS 管理 UI 静态资源根 (embed 的子目录)。
var uiFS, _ = fs.Sub(webFS, "web")

const (
	cookieName = "gcm_admin"
	sessionTTL = 7 * 24 * time.Hour
)

// Admin 本站管理员账号。
type Admin struct {
	ID           int64     `db:"id,omitempty"`
	Username     string    `db:"username"`
	PasswordHash string    `db:"password_hash"`
	SessionKey   string    `db:"session_key"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// Service 账号服务 — 每站点一个实例, 绑定本站 db。
type Service struct {
	db  *dba.SQL
	dao *dba.Dao[Admin]
}

// NewService 建账号服务。
func NewService(db *dba.SQL) *Service {
	return &Service{db: db, dao: dba.NewDao[Admin](db, "accounts")}
}

// DefaultCreated 首次引导生成的账号信息（密码只此一次可见）。
type DefaultCreated struct {
	Username string
	Password string
}

// EnsureDefaults 为本站库生成默认管理员（admin + 随机 16 位密码）;
// 已有账号跳过。站点项目启动时调用, 密码打印一次。
func EnsureDefaults(db *dba.SQL) (*DefaultCreated, error) {
	svc := NewService(db)
	ex, err := svc.Get()
	if err != nil {
		return nil, err
	}
	if ex != nil {
		return nil, nil
	}
	password, err := randomString(16)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	key, err := randomString(32)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := svc.dao.Create(&Admin{
		Username: "admin", PasswordHash: string(hash),
		SessionKey: key, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return nil, err
	}
	return &DefaultCreated{Username: "admin", Password: password}, nil
}

// SetPassword 强制设密（handler 负责先验旧密）。
func (s *Service) SetPassword(password string) error {
	if len(password) < 8 {
		return errors.New("admin: password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	affected, err := s.dao.Update(
		dba.H{"password_hash": string(hash), "updated_at": time.Now()}, "1 = 1")
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("admin: no account")
	}
	return nil
}

// VerifyPassword 验密（用户名+密码同时比对; 失败信息一致不泄露用户名）。
func (s *Service) VerifyPassword(username, password string) bool {
	a, err := s.Get()
	if err != nil || a == nil || a.Username != username {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) == nil
}

// NewSession 登录成功: 换新 session_key 并返回（cookie 值）。
func (s *Service) NewSession() (string, error) {
	key, err := randomString(32)
	if err != nil {
		return "", err
	}
	affected, err := s.dao.Update(
		dba.H{"session_key": key, "updated_at": time.Now()}, "1 = 1")
	if err != nil {
		return "", err
	}
	if affected == 0 {
		return "", errors.New("admin: no account")
	}
	return key, nil
}

// ValidSession 校验 cookie 值是否当前 session_key。
func (s *Service) ValidSession(key string) bool {
	if key == "" {
		return false
	}
	a, err := s.Get()
	return err == nil && a != nil && a.SessionKey == key
}

// Get 取本站账号; 未找到返回 (nil, nil)。
func (s *Service) Get() (*Admin, error) {
	return s.dao.Get("1 = 1")
}

func randomString(n int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var sb strings.Builder
	for range n {
		i, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		sb.WriteByte(chars[i.Int64()])
	}
	return sb.String(), nil
}
