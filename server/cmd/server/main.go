// Command server 是 Taffy 系统的中枢服务：
//   - 接收家具端 / 客户端的 WS / REST 请求
//   - 把家具端的语音 WS 会话转发给 Worker
//   - 用户 / 设备 CRUD、对话历史落库、MQTT 设备控制
//
// 不直接接 ASR / LLM / TTS（这些下沉到 worker/）。
//
// 启动：
//
//	go run ./cmd/server                                # 默认 :8080，读取 ./config.yaml
//	$env:PORT="8080"; $env:CONFIG_PATH="config.yaml"   # 可覆盖
//	$env:WORKER_WS_URL="ws://127.0.0.1:8090/v1/orchestrate"
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"taffy.local/pkg/httpx"

	"taffy-server/internal/config"
	"taffy-server/internal/database"
	"taffy-server/internal/handler"
	"taffy-server/internal/hub"
	"taffy-server/internal/middleware"
	"taffy-server/internal/mqtt"
	"taffy-server/internal/repository"
	"taffy-server/internal/service"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := httpx.Getenv("PORT", "8080")
	configPath := httpx.Getenv("CONFIG_PATH", "config.yaml")

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("config load failed, falling back to env-only defaults", "path", configPath, "err", err)
		cfg = config.DefaultConfig()
	}
	slog.Info("config ready", "worker_ws", cfg.Worker.WSURL, "mqtt_broker", cfg.MQTT.Broker)

	d := buildDeps(cfg)
	defer d.Close()

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           httpx.AccessLog(buildRouter(cfg, d)),
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

// deps 装配后的所有外部依赖，方便统一 Close。
type deps struct {
	db        *sql.DB
	rdb       *redis.Client
	mqtt      *mqtt.Bridge
	jwtMW     *middleware.JWTMiddleware
	hub       *hub.Hub
	userSvc   *service.UserService
	deviceSvc *service.DeviceService
	convSvc   *service.ConversationService
}

func (d *deps) Close() {
	if d.db != nil {
		_ = d.db.Close()
	}
	if d.rdb != nil {
		_ = d.rdb.Close()
	}
	if d.mqtt != nil {
		d.mqtt.Close()
	}
}

func buildDeps(cfg *config.AppConfig) *deps {
	d := &deps{}

	if db, err := database.InitMySQL(&cfg.MySQL); err != nil {
		slog.Warn("mysql init failed, running without database", "err", err)
	} else {
		d.db = db
	}

	if rdb, err := database.InitRedis(&cfg.Redis); err != nil {
		slog.Warn("redis init failed, running without cache", "err", err)
	} else {
		d.rdb = rdb
	}

	// JWT 中间件总是装配；rdb 为 nil 时黑名单功能自动降级。
	d.jwtMW = middleware.NewJWTMiddleware(&cfg.JWT, d.rdb)

	// Hub 始终启用——纯内存实现，无外部依赖。
	d.hub = hub.New(slog.Default())

	if d.db != nil {
		userRepo := repository.NewUserRepo(d.db)
		deviceRepo := repository.NewDeviceRepo(d.db)
		stateRepo := repository.NewDeviceStateRepo(d.db)
		convRepo := repository.NewConversationRepo(d.db)
		msgRepo := repository.NewMessageRepo(d.db)
		cmdRepo := repository.NewCommandRepo(d.db)

		d.userSvc = service.NewUserService(userRepo)
		d.deviceSvc = service.NewDeviceService(deviceRepo, stateRepo)
		d.convSvc = service.NewConversationService(convRepo, msgRepo, cmdRepo)
	}

	if cfg.MQTT.Broker != "" && d.deviceSvc != nil {
		bridge, err := mqtt.NewBridge(&cfg.MQTT, mqtt.Handlers{
			OnStatus:    service.NewMQTTStatusHandler(d.deviceSvc),
			OnResult:    service.NewMQTTResultHandler(d.convSvc),
			OnHeartbeat: service.NewMQTTHeartbeatHandler(d.deviceSvc),
		})
		if err != nil {
			slog.Warn("mqtt init failed, running without device bridge", "err", err)
		} else if bridge != nil {
			d.mqtt = bridge
			d.deviceSvc.AttachMQTT(mqttPublisherAdapter{bridge: bridge})
			slog.Info("mqtt bridge ready", "broker", cfg.MQTT.Broker)
		}
	}

	return d
}

// mqttPublisherAdapter 把 mqtt.Bridge 适配为 service.MQTTPublisher，切断反向依赖。
type mqttPublisherAdapter struct{ bridge *mqtt.Bridge }

func (a mqttPublisherAdapter) PublishCommand(ctx context.Context, deviceID string, p service.MQTTCommandPayload) error {
	return a.bridge.PublishGenericCommand(ctx, deviceID, p.ToolID, p.Action, p.Params)
}

func buildRouter(cfg *config.AppConfig, d *deps) http.Handler {
	mux := http.NewServeMux()

	authHandler := handler.NewAuthHandler(d.userSvc, d.jwtMW, cfg)
	deviceHandler := handler.NewDeviceHandler(d.deviceSvc, d.hub)
	voiceHandler := handler.NewVoiceHandler(cfg, d.deviceSvc, d.convSvc, d.hub)
	realtimeHandler := handler.NewRealtimeHandler(d.jwtMW, d.hub)

	var mqttProbe func() string
	if d.mqtt != nil {
		mqttProbe = d.mqtt.Status
	}
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		handler.HealthWithDB(w, r, cfg, d.db, d.rdb, mqttProbe)
	})

	mux.Handle("/v1/voice", voiceHandler)
	mux.Handle("/ws", realtimeHandler)

	mux.HandleFunc("/api/v1/auth/register", postOnly(authHandler.Register))
	mux.HandleFunc("/api/v1/auth/login", postOnly(authHandler.Login))

	mux.HandleFunc("POST /api/v1/auth/logout", d.jwtMW.RequireAuth(authHandler.Logout))

	if d.db != nil {
		mux.HandleFunc("/api/v1/devices", d.jwtMW.RequireAuth(deviceHandler.ListDevices))
		mux.HandleFunc("/api/v1/devices/states", d.jwtMW.RequireAuth(deviceHandler.ListDeviceStates))
		mux.HandleFunc("/api/v1/devices/state", methodOnly(http.MethodPut, d.jwtMW.RequireAuth(deviceHandler.UpdateDeviceState)))
		mux.HandleFunc("/api/v1/devices/{deviceID}/state", d.jwtMW.RequireAuth(deviceHandler.GetDeviceState))

		// UC-03 设备绑定 / 解绑 / 重命名
		mux.HandleFunc("GET /api/v1/devices/bindable", d.jwtMW.RequireAuth(deviceHandler.ListBindableDevices))
		mux.HandleFunc("POST /api/v1/devices/bind", d.jwtMW.RequireAuth(deviceHandler.BindDevice))
		mux.HandleFunc("PUT /api/v1/devices/{deviceID}", d.jwtMW.RequireAuth(deviceHandler.RenameDevice))
		mux.HandleFunc("DELETE /api/v1/devices/{deviceID}", d.jwtMW.RequireAuth(deviceHandler.UnbindDevice))
	}

	return mux
}

func postOnly(h http.HandlerFunc) http.HandlerFunc {
	return methodOnly(http.MethodPost, h)
}

func methodOnly(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}
