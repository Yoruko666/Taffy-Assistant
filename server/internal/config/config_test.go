package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateStartupAllowsDevelopmentDefault(t *testing.T) {
	t.Setenv("TAFFY_ENV", "")
	t.Setenv("JWT_SECRET", "")

	cfg := DefaultConfig()
	if cfg.Environment != "development" {
		t.Fatalf("environment = %q, want development", cfg.Environment)
	}
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("development config rejected: %v", err)
	}
	if cfg.JWT.Secret == "" {
		t.Fatal("development config should have an explicit development JWT secret")
	}
}

func TestValidateStartupRejectsMissingProductionSecret(t *testing.T) {
	t.Setenv("TAFFY_ENV", "production")
	t.Setenv("JWT_SECRET", "")

	cfg := DefaultConfig()
	cfg.JWT.Secret = ""
	if err := cfg.ValidateStartup(); err == nil {
		t.Fatal("production config with missing JWT secret was accepted")
	}
}

func TestValidateStartupRejectsWeakProductionSecret(t *testing.T) {
	cfg := &AppConfig{Environment: "prod", JWT: JWTConfig{Secret: "change-me-in-production"}}
	if err := cfg.ValidateStartup(); err == nil {
		t.Fatal("production config with weak JWT secret was accepted")
	}

	cfg.JWT.Secret = strings.Repeat("a", 31)
	if err := cfg.ValidateStartup(); err == nil {
		t.Fatal("production config with short JWT secret was accepted")
	}
}

func TestValidateStartupAcceptsStrongProductionSecret(t *testing.T) {
	cfg := &AppConfig{
		Environment: "production",
		JWT:         JWTConfig{Secret: "a-random-production-secret-with-at-least-32-bytes"},
	}
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("strong production secret rejected: %v", err)
	}
}

func TestLoadConfigEnvironmentAndSecretOverrides(t *testing.T) {
	t.Setenv("TAFFY_ENV", "production")
	t.Setenv("JWT_SECRET", "environment-secret-that-is-long-enough-123")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("environment: development\njwt:\n  secret: yaml-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Environment != "production" {
		t.Fatalf("environment = %q, want production", cfg.Environment)
	}
	if cfg.JWT.Secret != "environment-secret-that-is-long-enough-123" {
		t.Fatalf("JWT secret was not overridden by JWT_SECRET")
	}
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("overridden production config rejected: %v", err)
	}
}
