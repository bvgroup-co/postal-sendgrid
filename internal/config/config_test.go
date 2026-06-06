package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsMalformedEnvValues(t *testing.T) {
	baseEnv := map[string]string{
		"SHIM_AUTH_TOKEN":         "token",
		"POSTAL_BASE_URL":         "https://postal.example.com",
		"POSTAL_API_KEY":          "postal-key",
		"PLUNK_WEBHOOK_BASE_URL":  "https://plunk.example.com",
		"WEBHOOK_SIGNING_ENABLED": "false",
	}

	t.Run("integer", func(t *testing.T) {
		setBaseEnv(t, baseEnv)
		t.Setenv("MAIL_MAX_BYTES", "bad")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "MAIL_MAX_BYTES") {
			t.Fatalf("expected malformed MAIL_MAX_BYTES to fail, got %v", err)
		}
	})

	t.Run("attempts", func(t *testing.T) {
		setBaseEnv(t, baseEnv)
		t.Setenv("MAIL_MAX_BYTES", "1024")
		t.Setenv("FORWARD_ATTEMPTS", "abc")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "FORWARD_ATTEMPTS") {
			t.Fatalf("expected malformed FORWARD_ATTEMPTS to fail, got %v", err)
		}
	})

	t.Run("boolean", func(t *testing.T) {
		setBaseEnv(t, baseEnv)
		t.Setenv("MAIL_MAX_BYTES", "1024")
		t.Setenv("FORWARD_ATTEMPTS", "2")
		t.Setenv("WEBHOOK_SIGNING_ENABLED", "flase")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "WEBHOOK_SIGNING_ENABLED") {
			t.Fatalf("expected malformed WEBHOOK_SIGNING_ENABLED to fail, got %v", err)
		}
	})
}

func setBaseEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		t.Setenv(key, value)
	}
}
