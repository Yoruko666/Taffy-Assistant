// Package httpx 提供 server / worker 共用的 HTTP 通用工具：Getenv 与 AccessLog 中间件。
// 仅依赖标准库。
package httpx

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Getenv 返回环境变量 key 的值，未设置或为空时返回 def。
func Getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// AccessLog HTTP 中间件，给每个请求打一行 INFO 访问日志（method/path/remote/dur_ms）。
// WebSocket Upgrade 请求也会经过这里。
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"dur_ms", time.Since(t0).Milliseconds(),
		)
	})
}
