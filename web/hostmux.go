// HostMux 按域名分发到各站点（多站装配层用）。
package web

import (
	"net"
	"net/http"
	"strings"
)

// HostMux 按 Host 头分发 (design §4.1)。Host 仅作分发键是安全的;
// 绝对 URL 一律来自配置 base_url, 不从 r.Host 拼。
//
// 站点表只在装配期写 (cmx.New), 之后并发只读, 无需锁。
type HostMux struct {
	sites    map[string]http.Handler // normalizeHost(域名) → 站点 Cho
	fallback http.Handler            // default 站点: 未知 host / IP 直连
}

func NewHostMux() *HostMux {
	return &HostMux{sites: map[string]http.Handler{}}
}

// Add 注册一个站点的全部域名。
func (m *HostMux) Add(domains []string, h http.Handler) {
	for _, d := range domains {
		m.sites[normalizeHost(d)] = h
	}
}

// SetFallback 设置 default 站点 (配置里 default: true 恰好一个, 由 config 校验保证)。
func (m *HostMux) SetFallback(h http.Handler) { m.fallback = h }

func (m *HostMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 固定短路, 挂在 fallback 之前 (design §4.1)
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if h, ok := m.sites[normalizeHost(r.Host)]; ok {
		h.ServeHTTP(w, r)
		return
	}
	if m.fallback != nil {
		m.fallback.ServeHTTP(w, r)
		return
	}
	http.Error(w, "unknown host", http.StatusNotFound)
}

// normalizeHost 去端口、小写、去尾点。
func normalizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
