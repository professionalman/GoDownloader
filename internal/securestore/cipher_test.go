package securestore

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestAESGCMFieldBindingAndFreshNonce(t *testing.T) {
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	cipher, err := NewFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.Encrypt("settings", "global", "proxy_password", "secret")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := cipher.Encrypt("settings", "global", "proxy_password", "secret")
	if string(first) == string(second) {
		t.Fatal("ciphertexts must use fresh nonces")
	}
	plain, err := cipher.Decrypt("settings", "global", "proxy_password", first)
	if err != nil || plain != "secret" {
		t.Fatalf("decrypt=%q, %v", plain, err)
	}
	if _, err := cipher.Decrypt("job", "global", "proxy_password", first); err == nil {
		t.Fatal("AAD must bind scope, owner and field")
	}
}

func TestMissingKeyIsNonFatalButUnavailable(t *testing.T) {
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", "")
	cipher, err := NewFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if cipher.Available() {
		t.Fatal("cipher should be unavailable without a key")
	}
	if _, err := cipher.Encrypt("a", "b", "c", "secret"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}
