package common

import (
	"testing"

	"done-hub/common/config"

	"github.com/spf13/viper"
)

func prepareUserTokenTest(t *testing.T, sessionSecret string) {
	t.Helper()

	oldSessionSecret := config.SessionSecret
	viper.Reset()
	config.SessionSecret = sessionSecret

	t.Cleanup(func() {
		viper.Reset()
		config.SessionSecret = oldSessionSecret
	})
}

func TestResolveUserTokenSecretPriority(t *testing.T) {
	prepareUserTokenTest(t, "session-from-config")
	viper.Set("session_secret", "session-from-viper")
	viper.Set("token_secret", "token-secret")
	viper.Set("user_token_secret", "user-token-secret")

	if got := resolveUserTokenSecret(); got != "user-token-secret" {
		t.Fatalf("expected user_token_secret, got %q", got)
	}
}

func TestResolveUserTokenSecretFallbackToLegacyTokenSecret(t *testing.T) {
	prepareUserTokenTest(t, "session-from-config")
	viper.Set("token_secret", "legacy-token-secret")

	if got := resolveUserTokenSecret(); got != "legacy-token-secret" {
		t.Fatalf("expected token_secret fallback, got %q", got)
	}
}

func TestResolveUserTokenSecretFallbackToSessionSecret(t *testing.T) {
	prepareUserTokenTest(t, "session-from-config")
	viper.Set("session_secret", "session-from-viper")

	if got := resolveUserTokenSecret(); got != "session-from-viper" {
		t.Fatalf("expected session_secret fallback, got %q", got)
	}
}

func TestInitUserTokenCanStartWithoutDedicatedUserTokenSecret(t *testing.T) {
	prepareUserTokenTest(t, "session-from-config")

	if err := InitUserToken(); err != nil {
		t.Fatalf("expected InitUserToken to succeed with session secret fallback, got error: %v", err)
	}
}
