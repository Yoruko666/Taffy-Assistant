// Package handler 保留 LoadConfig / DefaultConfig / AppConfig 的向后兼容导出。
// 实际定义已迁移到 internal/config 包，以消除循环导入。
package handler

import "system/internal/config"

// AppConfig 是 config.AppConfig 的类型别名，保持向后兼容。
type AppConfig = config.AppConfig

// LoadConfig 从 YAML 文件加载 server 配置。
func LoadConfig(path string) (*config.AppConfig, error) {
	return config.LoadConfig(path)
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *config.AppConfig {
	return config.DefaultConfig()
}
