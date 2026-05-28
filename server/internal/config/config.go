// Package config 定义 Taffy Server 的配置结构。
// 独立于 handler / database 包，避免循环导入。
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkerConfig 描述 Worker（大模型编排进程）的接入信息。
type WorkerConfig struct {
	WSURL string `yaml:"ws_url"`
}

// MySQLConfig MySQL 连接配置。
type MySQLConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	DBName          string `yaml:"dbname"`
	MaxOpenConns    int    `yaml:"max_open_conns"`
	MaxIdleConns    int    `yaml:"max_idle_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime"`
}

// DSN 返回 MySQL 数据源名称。
func (c *MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=Local",
		c.User, c.Password, c.Host, c.Port, c.DBName)
}

// ConnMaxLifetimeDuration 解析连接最大生命周期。
func (c *MySQLConfig) ConnMaxLifetimeDuration() time.Duration {
	d, err := time.ParseDuration(c.ConnMaxLifetime)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// RedisConfig Redis 连接配置。
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// JWTConfig JWT 鉴权配置。
type JWTConfig struct {
	Secret     string `yaml:"secret"`
	AccessTTL  string `yaml:"access_ttl"`
	RefreshTTL string `yaml:"refresh_ttl"`
}

// AccessTTLDuration 解析 Access Token 有效期。
func (c *JWTConfig) AccessTTLDuration() time.Duration {
	d, err := time.ParseDuration(c.AccessTTL)
	if err != nil {
		return 2 * time.Hour
	}
	return d
}

// RefreshTTLDuration 解析 Refresh Token 有效期。
func (c *JWTConfig) RefreshTTLDuration() time.Duration {
	d, err := time.ParseDuration(c.RefreshTTL)
	if err != nil {
		return 7 * 24 * time.Hour
	}
	return d
}

// AppConfig Server 顶层配置。
type AppConfig struct {
	Worker WorkerConfig `yaml:"worker"`
	MySQL  MySQLConfig  `yaml:"mysql"`
	Redis  RedisConfig  `yaml:"redis"`
	JWT    JWTConfig    `yaml:"jwt"`
}

// setDefaults 填充零值字段为合理的默认值。
// 环境变量优先级高于 YAML 中的值，便于容器化部署。
func (c *AppConfig) setDefaults() {
	if c.Worker.WSURL == "" {
		c.Worker.WSURL = "ws://127.0.0.1:8090/v1/orchestrate"
	}
	if v := os.Getenv("WORKER_WS_URL"); v != "" {
		c.Worker.WSURL = v
	}
	if c.MySQL.Host == "" {
		c.MySQL.Host = "127.0.0.1"
	}
	if c.MySQL.Port == 0 {
		c.MySQL.Port = 3306
	}
	if c.MySQL.DBName == "" {
		c.MySQL.DBName = "taffy"
	}
	if c.MySQL.MaxOpenConns == 0 {
		c.MySQL.MaxOpenConns = 20
	}
	if c.MySQL.MaxIdleConns == 0 {
		c.MySQL.MaxIdleConns = 10
	}
	if c.MySQL.ConnMaxLifetime == "" {
		c.MySQL.ConnMaxLifetime = "5m"
	}
	if v := os.Getenv("MYSQL_HOST"); v != "" {
		c.MySQL.Host = v
	}
	if v := os.Getenv("MYSQL_PASSWORD"); v != "" {
		c.MySQL.Password = v
	}
	if c.Redis.Addr == "" {
		c.Redis.Addr = "127.0.0.1:6379"
	}
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		c.Redis.Addr = v
	}
	if c.JWT.Secret == "" {
		c.JWT.Secret = "change-me-in-production"
	}
	if c.JWT.AccessTTL == "" {
		c.JWT.AccessTTL = "2h"
	}
	if c.JWT.RefreshTTL == "" {
		c.JWT.RefreshTTL = "168h"
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		c.JWT.Secret = v
	}
}

// LoadConfig 从 YAML 文件加载 server 配置。
func LoadConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.setDefaults()
	return &cfg, nil
}

// DefaultConfig 返回一个仅包含默认值（含环境变量覆盖）的配置，
// 用于 YAML 文件不存在 / 解析失败时的降级。
func DefaultConfig() *AppConfig {
	cfg := &AppConfig{}
	cfg.setDefaults()
	return cfg
}
