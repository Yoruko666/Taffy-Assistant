// Command server 是 Taffy 系统的 Go 中枢服务。
//
// 拆分后职责：
//   - 接收家具端 / 客户端的接入（WS / REST）
//   - 把家具端的语音 WS 会话整段转发给 Worker（大模型编排进程）
//   - 用户/设备 CRUD、对话历史落库、MQTT 设备控制
//
// Server **不直接** 接 ASR / LLM / TTS，相关配置已搬到 worker/config.yaml。
//
// 启动：
//
//	# 默认监听 :8080，读取同目录 config.yaml
//	go run ./cmd/server
//
//	# 自定义
//	$env:PORT="8080"
//	$env:CONFIG_PATH="config.yaml"
//	$env:WORKER_WS_URL="ws://127.0.0.1:8090/v1/orchestrate"
//	go run ./cmd/server
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

	"taffy-server/internal/config"
	"taffy-server/internal/database"
	"taffy-server/internal/handler"
	"taffy-server/internal/middleware"
	"taffy-server/internal/repository"
	"taffy-server/internal/service"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := getenv("PORT", "8080")
	configPath := getenv("CONFIG_PATH", "config.yaml")

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("config load failed, falling back to env-only defaults", "path", configPath, "err", err)
		cfg = config.DefaultConfig()
	}
	slog.Info("config ready", "worker_ws", cfg.Worker.WSURL)

	// ──────────── 初始化 MySQL ────────────
	db, err := database.InitMySQL(&cfg.MySQL)
	if err != nil {
		slog.Warn("mysql init failed, running without database", "err", err)
	} else {
		defer db.Close()
	}

	// ──────────── 初始化 Redis ────────────
	rdb, err := database.InitRedis(&cfg.Redis)
	if err != nil {
		slog.Warn("redis init failed, running without cache", "err", err)
	} else {
		defer rdb.Close()
	}

	// ──────────── 组装依赖 ────────────
	jwtMW := middleware.NewJWTMiddleware(&cfg.JWT, rdb)

	var userSvc *service.UserService
	var deviceSvc *service.DeviceService
	if db != nil {
		userRepo := repository.NewUserRepo(db)
		deviceRepo := repository.NewDeviceRepo(db)
		stateRepo := repository.NewDeviceStateRepo(db)

		userSvc = service.NewUserService(userRepo)
		deviceSvc = service.NewDeviceService(deviceRepo, stateRepo)
	}

	authHandler := handler.NewAuthHandler(userSvc, jwtMW, cfg)
	deviceHandler := handler.NewDeviceHandler(deviceSvc)
	voiceHandler := handler.NewVoiceHandler(cfg, deviceSvc)

	// ──────────── 路由注册 ────────────
	mux := http.NewServeMux()

	// 健康检查
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		handler.HealthWithDB(w, r, cfg, db, rdb)
	})

	// 语音透传（家具端 WS）
	mux.Handle("/v1/voice", voiceHandler)

	// 认证（无需鉴权）
	mux.HandleFunc("/api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		authHandler.Register(w, r)
	})
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		authHandler.Login(w, r)
	})

	// 设备 API（需鉴权）
	if db != nil {
		mux.HandleFunc("/api/v1/devices", jwtMW.RequireAuth(deviceHandler.ListDevices))
		mux.HandleFunc("/api/v1/devices/states", jwtMW.RequireAuth(deviceHandler.ListDeviceStates))
		mux.HandleFunc("/api/v1/devices/state", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				jwtMW.RequireAuth(deviceHandler.UpdateDeviceState).ServeHTTP(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
		mux.HandleFunc("/api/v1/devices/{deviceID}/state", jwtMW.RequireAuth(deviceHandler.GetDeviceState))
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           withAccessLog(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("server starting", "addr", srv.Addr, "worker_ws", cfg.Worker.WSURL)
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
