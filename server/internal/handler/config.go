package handler

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WorkerConfig 描述 Worker（大模型编排进程）的接入信息。
// 拆分后 Server 不再直接访问 ASR / LLM / TTS，全部通过 Worker 编排。
type WorkerConfig struct {
	WSURL string `yaml:"ws_url"`
}

// AppConfig Server 顶层配置。
type AppConfig struct {
	Worker WorkerConfig `yaml:"worker"`
}

// setDefaults 填充零值字段为合理的默认值。
// 环境变量 WORKER_WS_URL 优先级高于 YAML 中的值，便于容器化部署。
func (c *AppConfig) setDefaults() {
	if c.Worker.WSURL == "" {
		c.Worker.WSURL = "ws://127.0.0.1:8090/v1/orchestrate"
	}
	if v := os.Getenv("WORKER_WS_URL"); v != "" {
		c.Worker.WSURL = v
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
