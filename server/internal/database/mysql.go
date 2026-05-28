package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"taffy-server/internal/config"
)

// InitMySQL 根据 AppConfig 初始化 MySQL 连接池。
func InitMySQL(cfg *config.MySQLConfig) (*sql.DB, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetimeDuration())

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	slog.Info("mysql connected", "host", cfg.Host, "port", cfg.Port, "db", cfg.DBName)
	return db, nil
}

// HealthCheck 检查 MySQL 连接是否可用，返回状态字符串。
func HealthCheck(db *sql.DB) string {
	if db == nil {
		return "not_configured"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return "error"
	}
	return "ok"
}
