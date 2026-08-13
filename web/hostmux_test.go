package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 域名分发 + fallback + normalizeHost。
func TestHostMux(t *testing.T) {
	m := NewHostMux()
	ok := func(s string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(s)) })
	}
	m.Add([]string{"a.com"}, ok("A"))
	m.Add([]string{"B.com:8080"}, ok("B"))
	m.SetFallback(ok("F"))

	hit := func(host string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w.Body.String()
	}
	if hit("a.com") != "A" || hit("b.com") != "B" || hit("unknown.com") != "F" || hit("1.2.3.4") != "F" {
		t.Fatal("host routing broken")
	}
	if normalizeHost("X.COM:443") != "x.com" || normalizeHost("a.com.") != "a.com" {
		t.Fatal("normalizeHost broken")
	}
}
