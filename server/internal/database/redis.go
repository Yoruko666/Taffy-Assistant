package database

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"taffy-server/internal/config"
)

// InitRedis 根据 AppConfig 初始化 Redis 连接。
func InitRedis(cfg *config.RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	slog.Info("redis connected", "addr", cfg.Addr)
	return rdb, nil
}

// RedisHealthCheck 检查 Redis 连接是否可用。
func RedisHealthCheck(rdb *redis.Client) string {
	if rdb == nil {
		return "not_configured"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return "error"
	}
	return "ok"
}
