package config

import (
	"os"
	"testing"
)

func TestConfig_QBitMetadataTimeout(t *testing.T) {
	// 1. Default should be 300
	os.Unsetenv("QBIT_METADATA_TIMEOUT_SECONDS")
	cfgDefault := New()
	if cfgDefault.QBitMetadataTimeoutSeconds != 300 {
		t.Errorf("expected default timeout 300, got %d", cfgDefault.QBitMetadataTimeoutSeconds)
	}

	// 2. Below minimum (<10) should reset to default 300 in production config
	os.Setenv("QBIT_METADATA_TIMEOUT_SECONDS", "5")
	cfgLow := New()
	if cfgLow.QBitMetadataTimeoutSeconds != 300 {
		t.Errorf("expected low timeout (<10) to reset to 300, got %d", cfgLow.QBitMetadataTimeoutSeconds)
	}

	// 3. Above maximum (>3600) should clamp to 3600
	os.Setenv("QBIT_METADATA_TIMEOUT_SECONDS", "5000")
	cfgHigh := New()
	if cfgHigh.QBitMetadataTimeoutSeconds != 3600 {
		t.Errorf("expected high timeout (>3600) to clamp to 3600, got %d", cfgHigh.QBitMetadataTimeoutSeconds)
	}

	// 4. Valid value (e.g. 600)
	os.Setenv("QBIT_METADATA_TIMEOUT_SECONDS", "600")
	cfgValid := New()
	if cfgValid.QBitMetadataTimeoutSeconds != 600 {
		t.Errorf("expected valid timeout 600, got %d", cfgValid.QBitMetadataTimeoutSeconds)
	}

	os.Unsetenv("QBIT_METADATA_TIMEOUT_SECONDS")
}
