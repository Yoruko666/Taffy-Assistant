// Command server 是 SHVA 系统的 Go 中枢服务（M1：仅健康检查 + /v1/voice 透传 ASR）。
//
// 启动：
//
//	# 默认监听 :8080，转发 ASR 到 ws://127.0.0.1:9100/v1/asr/stream
//	go run ./cmd/server
//
//	# 自定义
//	$env:PORT="8080"; $env:ASR_WS_URL="ws://127.0.0.1:9100/v1/asr/stream"; go run ./cmd/server
//
// 联调：
//
//	python ../model/asr_server/scripts/test_stream.py \
//	    --url "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1" \
//	    --wav some.wav
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"system/internal/handler"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := getenv("PORT", "8080")
	asrWS := getenv("ASR_WS_URL", "ws://127.0.0.1:9100/v1/asr/stream")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", handler.Health)
	mux.Handle("/v1/voice", handler.NewVoiceHandler(asrWS))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           withAccessLog(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 启动
	go func() {
		slog.Info("server starting", "addr", srv.Addr, "asr_ws", asrWS)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	// 优雅退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("bye")
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// withAccessLog 给所有 HTTP 请求打一行访问日志（WebSocket 升级前也会经过这里）。
func withAccessLog(next http.Handler) http.Handler {
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
