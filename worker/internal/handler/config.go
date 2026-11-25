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

// LLMProvider 单个大模型提供商配置。
type LLMProvider struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// LLMConfig 大模型 API 配置（通过本地 llm_server 代理 CodeBuddy SDK）。
type LLMConfig struct {
	// Providers are tried in order; the first provider is the primary one.
	Providers           []LLMProvider `yaml:"providers"`
	// Deprecated legacy fields are retained for compatibility with older YAML.
	// New configuration should use Providers instead.
	URL                 string `yaml:"url"`
	APIKey              string `yaml:"api_key"`
	Model               string `yaml:"model"`
	InternetEnvironment string `yaml:"internet_environment"`
	SystemPrompt        string `yaml:"system_prompt"`
	Timeout             int    `yaml:"timeout"` // 秒
}

// TTSConfig TTS 模型 HTTP 接入配置。
type TTSConfig struct {
	URL string `yaml:"url"`
}

// AppConfig Worker 顶层配置。
type AppConfig struct {
	ASR ASRConfig `yaml:"asr"`
	LLM LLMConfig `yaml:"llm"`
	TTS TTSConfig `yaml:"tts"`
}

// setDefaults 用合理默认值填充零值字段。
// 环境变量 LLM_API_KEY / LLM_URL / LLM_MODEL 作为第 0 个 provider 的覆盖（仅对 providers[0] 生效）。
// 环境变量 ASR_WS_URL / TTS_URL 覆盖对应配置。
func (c *AppConfig) setDefaults() {
	if c.ASR.WSURL == "" {
		c.ASR.WSURL = "ws://127.0.0.1:9100/v1/asr/stream"
	}
	if v := os.Getenv("ASR_WS_URL"); v != "" {
		c.ASR.WSURL = v
	}

	if c.LLM.Timeout <= 0 {
		c.LLM.Timeout = 30
	}

	// 兼容旧版只有 llm.url/api_key/model 的配置；统一转换为第一个 provider。
	if len(c.LLM.Providers) == 0 && (c.LLM.URL != "" || c.LLM.APIKey != "" || c.LLM.Model != "") {
		c.LLM.Providers = append(c.LLM.Providers, LLMProvider{
			URL:    c.LLM.URL,
			APIKey: c.LLM.APIKey,
			Model:  c.LLM.Model,
		})
	}

	// 环境变量始终覆盖 providers[0] 的对应字段；没有 YAML provider 时，
	// 只要任一 LLM 环境变量存在就创建第一个 provider。
	if len(c.LLM.Providers) == 0 {
		if os.Getenv("LLM_URL") != "" || os.Getenv("LLM_API_KEY") != "" || os.Getenv("LLM_MODEL") != "" {
			c.LLM.Providers = append(c.LLM.Providers, LLMProvider{})
		}
	}
	if len(c.LLM.Providers) > 0 {
		if v := os.Getenv("LLM_API_KEY"); v != "" {
			c.LLM.Providers[0].APIKey = v
		}
		if v := os.Getenv("LLM_URL"); v != "" {
			c.LLM.Providers[0].URL = v
		}
		if v := os.Getenv("LLM_MODEL"); v != "" {
			c.LLM.Providers[0].Model = v
		}
	}

	if v := os.Getenv("TTS_URL"); v != "" {
		c.TTS.URL = v
	}
}

// PrimaryProvider 返回第一个可用的 LLM provider，如果没有则返回零值。
func (c *LLMConfig) PrimaryProvider() (LLMProvider, bool) {
	if len(c.Providers) == 0 {
		return LLMProvider{}, false
	}
	return c.Providers[0], true
}

// FallbackProviders 返回第一个之后的所有备用 provider。
func (c *LLMConfig) FallbackProviders() []LLMProvider {
	if len(c.Providers) <= 1 {
		return nil
	}
	return c.Providers[1:]
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

// DefaultConfig 返回仅含默认值（含环境变量覆盖）的配置，
// 用于 YAML 文件不存在或解析失败时的降级。
func DefaultConfig() *AppConfig {
	cfg := &AppConfig{}
	cfg.setDefaults()
	return cfg
}
