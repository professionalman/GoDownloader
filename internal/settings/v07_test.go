package settings_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"downloader/internal/database"
	"downloader/internal/securestore"
	"downloader/internal/settings"
)

func TestV07SecretMaskingEncryptionAndEnvironmentPrecedence(t *testing.T) {
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	tempDir := t.TempDir()
	db, err := database.New(filepath.Join(tempDir, "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cipher, err := securestore.NewFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	store := securestore.NewStore(database.NewSQLiteSecretRepository(db), cipher)
	service := settings.NewSettingsService(database.NewSQLiteSettingsRepository(db), tempDir, tempDir, store)

	var request settings.UpdateSettingsRequest
	if err := json.Unmarshal([]byte(`{
		"network":{
			"proxy":{"mode":"custom","protocol":"http","host":"proxy.local","port":8080,"username":"user"},
			"proxyPassword":"plaintext-password",
			"httpHeaders":[{"name":"Authorization","value":"Bearer plaintext-token"}]
		}
	}`), &request); err != nil {
		t.Fatal(err)
	}
	got, err := service.UpdatePowerSettings(context.Background(), &request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "plaintext-password") || strings.Contains(string(encoded), "plaintext-token") {
		t.Fatalf("secret leaked in response: %s", encoded)
	}
	if !got.Network.Proxy.HasPassword || !got.Network.HTTPHeaders[0].HasValue || got.Network.HTTPHeaders[0].Value != "" {
		t.Fatalf("missing secret markers: %+v", got.Network)
	}

	var preserve settings.UpdateSettingsRequest
	if err := json.Unmarshal([]byte(`{
		"network":{
			"proxy":{"mode":"custom","protocol":"http","host":"proxy.local","port":8080,"username":"user"},
			"httpHeaders":[{"name":"Authorization","sensitive":true,"hasValue":true}]
		}
	}`), &preserve); err != nil {
		t.Fatal(err)
	}
	got, err = service.UpdatePowerSettings(context.Background(), &preserve)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Network.Proxy.HasPassword || !got.Network.HTTPHeaders[0].HasValue {
		t.Fatalf("empty secret update did not preserve configured values: %+v", got.Network)
	}

	var clear settings.UpdateSettingsRequest
	if err := json.Unmarshal([]byte(`{
		"network":{
			"proxy":{"mode":"custom","protocol":"http","host":"proxy.local","port":8080,"username":"user"},
			"clearProxyPassword":true,
			"httpHeaders":[{"name":"Authorization","clearValue":true}]
		}
	}`), &clear); err != nil {
		t.Fatal(err)
	}
	got, err = service.UpdatePowerSettings(context.Background(), &clear)
	if err != nil {
		t.Fatal(err)
	}
	if got.Network.Proxy.HasPassword || got.Network.HTTPHeaders[0].HasValue {
		t.Fatalf("explicit clear retained configured values: %+v", got.Network)
	}

	t.Setenv("GLOBAL_DOWNLOAD_LIMIT_BYTES_PER_SECOND", "1234")
	got, err = service.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Network.GlobalDownloadLimitBytesPerSecond != 1234 || !got.Overrides["network.globalDownloadLimitBytesPerSecond"] {
		t.Fatalf("environment precedence missing: %+v", got.Network)
	}
}

func TestInvalidEnvironmentOverrideIsIgnoredWithoutEchoingValue(t *testing.T) {
	service, db := setupTestSettings(t)
	defer db.Close()
	t.Setenv("GLOBAL_DOWNLOAD_LIMIT_BYTES_PER_SECOND", "not-a-number-secretish")
	got, err := service.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Network.GlobalDownloadLimitBytesPerSecond != 0 || len(got.ApplicationResults) != 1 {
		t.Fatalf("unexpected settings: %+v", got)
	}
	if strings.Contains(got.ApplicationResults[0].Message, "not-a-number-secretish") {
		t.Fatal("invalid environment value leaked into warning")
	}
}
