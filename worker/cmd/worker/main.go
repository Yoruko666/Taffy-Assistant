// Command worker 是 SHVA 系统的大模型编排进程。
//
// 职责：
//   - 接受 Server 通过 WS 推送的语音流（/v1/orchestrate）
//   - 调用本地 ASR（FunASR）做流式识别
//   - asr_final 触发云端 LLM 调用
//   - 未来扩展：TTS 合成、设备指令解析、并行设备控制
//
// Worker 与 Server 之间用 WS 长连接复用一次会话，协议与"家具端 ↔ Server"完全相同。
//
// 启动：
//
//	# 默认监听 :8090，读取同目录 config.yaml
//	go run ./cmd/worker
//
//	# 自定义
//	$env:WORKER_PORT="8090"; $env:CONFIG_PATH="config.yaml"; go run ./cmd/worker
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

	"worker/internal/handler"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := getenv("WORKER_PORT", "8090")
	configPath := getenv("CONFIG_PATH", "config.yaml")

	cfg, err := handler.LoadConfig(configPath)
	if err != nil {
		slog.Warn("config load failed, falling back to env-only defaults", "path", configPath, "err", err)
		cfg = handler.DefaultConfig()
	}
	slog.Info("config ready",
		"asr_ws", cfg.ASR.WSURL,
		"llm_url", cfg.LLM.URL,
		"llm_model", cfg.LLM.Model,
		"tts_url", cfg.TTS.URL,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		handler.HealthWithConfig(w, r, cfg)
	})
	mux.Handle("/v1/orchestrate", handler.NewOrchestrateHandler(cfg))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           withAccessLog(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("worker starting", "addr", srv.Addr, "asr_ws", cfg.ASR.WSURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

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
