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

