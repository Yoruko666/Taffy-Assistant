package handler

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ASRConfig ASR 模型 WS 接入配置。
type ASRConfig struct {
	WSURL string `yaml:"ws_url"`
}

// LLMConfig 大模型 API 配置（兼容 OpenAI Chat Completions 格式）。
type LLMConfig struct {
	URL          string `yaml:"url"`
	APIKey       string `yaml:"api_key"`
	Model        string `yaml:"model"`
	SystemPrompt string `yaml:"system_prompt"`
	Timeout      int    `yaml:"timeout"` // 秒
}

// TTSConfig TTS 模型 HTTP 接入配置（M3 接入）。
type TTSConfig struct {
	URL string `yaml:"url"`
}

// AppConfig Worker 顶层配置。
type AppConfig struct {
	ASR ASRConfig `yaml:"asr"`
	LLM LLMConfig `yaml:"llm"`
	TTS TTSConfig `yaml:"tts"`
}

// setDefaults 填充零值字段为合理的默认值。
// APIKey 优先从环境变量 LLM_API_KEY 读取，其次才用 YAML 中的值。
// ASR_WS_URL / TTS_URL 同样支持环境变量覆盖，便于容器化部署。
func (c *AppConfig) setDefaults() {
	if c.ASR.WSURL == "" {
		c.ASR.WSURL = "ws://127.0.0.1:9100/v1/asr/stream"
	}
	if v := os.Getenv("ASR_WS_URL"); v != "" {
		c.ASR.WSURL = v
	}

	if c.LLM.Model == "" {
		c.LLM.Model = "gpt-3.5-turbo"
	}
	if c.LLM.Timeout <= 0 {
		c.LLM.Timeout = 30
	}
	if envKey := os.Getenv("LLM_API_KEY"); envKey != "" {
		c.LLM.APIKey = envKey
	}

	if v := os.Getenv("TTS_URL"); v != "" {
		c.TTS.URL = v
	}
}

// LoadConfig 从 YAML 文件加载 worker 配置。
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
