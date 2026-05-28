package database

import (
	"database/sql"
	"fmt"
	"log/slog"

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

