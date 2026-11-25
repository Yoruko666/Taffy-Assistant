package wsutil

import (
	"net/http/httptest"
	"testing"
)

func TestDefaultOriginPolicy(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "non-browser empty origin", host: "localhost:8080", want: true},
		{name: "same origin", origin: "http://localhost:8080", host: "localhost:8080", want: true},
		{name: "cross origin", origin: "https://evil.example", host: "localhost:8080", want: false},
		{name: "malformed origin", origin: "not an origin", host: "localhost:8080", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tt.host+"/ws", nil)
			r.Host = tt.host
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := DefaultOriginPolicy(r); got != tt.want {
				t.Fatalf("DefaultOriginPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllowOrigins(t *testing.T) {
	policy := AllowOrigins("https://app.example")

	for _, tt := range []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "configured origin", origin: "https://app.example", want: true},
		{name: "unconfigured origin", origin: "https://other.example", want: false},
		{name: "empty origin remains non-browser", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost/ws", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := policy(r); got != tt.want {
				t.Fatalf("AllowOrigins() policy = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultUpgraderUsesConfiguredOriginPolicy(t *testing.T) {
	upgrader := DefaultUpgrader(AllowOrigins("https://app.example"))
	if upgrader.CheckOrigin == nil {
		t.Fatal("DefaultUpgrader() returned nil CheckOrigin")
	}

	r := httptest.NewRequest("GET", "http://localhost/ws", nil)
	r.Header.Set("Origin", "https://other.example")
	if upgrader.CheckOrigin(r) {
		t.Fatal("configured policy accepted an unconfigured Origin")
	}
}
