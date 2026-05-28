// Command worker 是 Taffy 系统的大模型编排进程。
//
// 职责：
//   - 接收 Server 通过 WS 推送的语音流（/v1/orchestrate）
//   - 调用本地 ASR 做流式识别
//   - asr_final 触发云端 LLM 调用（OpenAI Function Calling）
//   - LLM tool_calls 转发给 server 执行设备控制，结果回填二轮 LLM
//   - 异步合成 TTS 并下发 tts_audio
//
// 启动：
//
//	go run ./cmd/worker                                # 默认 :8090
//	$env:WORKER_PORT="8090"; $env:CONFIG_PATH="config.yaml"  # 可覆盖
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

	"taffy.local/pkg/httpx"

	"taffy-worker/internal/handler"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := httpx.Getenv("WORKER_PORT", "8090")
	configPath := httpx.Getenv("CONFIG_PATH", "config.yaml")

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
		Handler:           httpx.AccessLog(mux),
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
