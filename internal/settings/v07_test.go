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

	t.Setenv("GLOBAL_DOWNLOAD_LIMIT_BYTES_PER_SECOND", "1234")
	got, err = service.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Network.GlobalDownloadLimitBytesPerSecond != 1234 || !got.Overrides["network.globalDownloadLimitBytesPerSecond"] {
		t.Fatalf("environment precedence missing: %+v", got.Network)
	}
}
