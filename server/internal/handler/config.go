package handler

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LLMConfig 大模型 API 配置（兼容 OpenAI Chat Completions 格式）。
type LLMConfig struct {
	URL           string `yaml:"url"`
	APIKey        string `yaml:"api_key"`
	Model         string `yaml:"model"`
	SystemPrompt  string `yaml:"system_prompt"`
	Timeout       int    `yaml:"timeout"` // 秒
}

// AppConfig 应用顶层配置。
type AppConfig struct {
	LLM LLMConfig `yaml:"llm"`
}

// setDefaults 填充零值字段为合理的默认值。
// APIKey 优先从环境变量 LLM_API_KEY 读取，其次才用 YAML 中的值。
func (c *AppConfig) setDefaults() {
	if c.LLM.Model == "" {
		c.LLM.Model = "gpt-3.5-turbo"
	}
	if c.LLM.Timeout <= 0 {
		c.LLM.Timeout = 30
	}
	// 环境变量优先级高于配置文件
	if envKey := os.Getenv("LLM_API_KEY"); envKey != "" {
		c.LLM.APIKey = envKey
	}
}

// LoadConfig 从 YAML 文件加载配置。
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
