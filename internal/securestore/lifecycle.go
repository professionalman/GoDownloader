package securestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	// DefaultTargetName is the Windows Credential Manager target name for GoDownloader.
	DefaultTargetName = "GoDownloader/MasterKey"

	// DefaultLegacyEnvVar is the legacy V0.7 environment variable for the encryption key.
	DefaultLegacyEnvVar = "V0.7_SETTINGS_ENCRYPTION_KEY"
)

// KeyStatus represents the resolution provenance of the master key.
type KeyStatus string

const (
	// KeyStatusLoadedFromOS indicates the key was retrieved from the OS credential store.
	KeyStatusLoadedFromOS KeyStatus = "loaded_from_os"

	// KeyStatusMigratedFromEnv indicates the key was read from legacy environment and saved to OS store.
	KeyStatusMigratedFromEnv KeyStatus = "migrated_from_env"

	// KeyStatusGeneratedFresh indicates a fresh random key was generated and saved to OS store.
	KeyStatusGeneratedFresh KeyStatus = "generated_fresh"

	// KeyStatusEnvFallback indicates the key was loaded from environment because OS provider was unavailable.
	KeyStatusEnvFallback KeyStatus = "env_fallback"
)

// MasterKeyManager orchestrates the lifecycle, migration, and resolution of the master key.
type MasterKeyManager struct {
	provider   KeyProvider
	targetName string
	legacyEnv  string
}

// NewMasterKeyManager creates a manager with custom provider, target, and legacy environment variable name.
func NewMasterKeyManager(provider KeyProvider, targetName, legacyEnv string) *MasterKeyManager {
	if provider == nil {
		provider = NewDefaultKeyProvider()
	}
	if targetName == "" {
		targetName = DefaultTargetName
	}
	if legacyEnv == "" {
		legacyEnv = DefaultLegacyEnvVar
	}
	return &MasterKeyManager{
		provider:   provider,
		targetName: targetName,
		legacyEnv:  legacyEnv,
	}
}

// NewDefaultMasterKeyManager creates a MasterKeyManager using the platform default provider and target.
func NewDefaultMasterKeyManager() *MasterKeyManager {
	target := os.Getenv("GODOWNLOADER_KEY_TARGET")
	if target == "" {
		target = DefaultTargetName
	}
	return NewMasterKeyManager(NewDefaultKeyProvider(), target, DefaultLegacyEnvVar)
}

// ProviderName returns the name of the underlying key provider.
func (m *MasterKeyManager) ProviderName() string {
	if m.provider == nil {
		return "none"
	}
	return m.provider.Name()
}

// TargetName returns the configured target credential identifier.
func (m *MasterKeyManager) TargetName() string {
	return m.targetName
}

// validateKeyAgainstCiphertext cryptographically verifies that a candidate 32-byte key
// successfully authenticates and decrypts all existing encrypted records in the repository.
// Returns ErrKeyMismatch if any record fails decryption.
// Returns nil if no records exist or all records decrypt successfully.
// Does NOT log or leak decrypted plaintext, keys, or ciphertext.
func validateKeyAgainstCiphertext(ctx context.Context, repo Repository, key []byte) error {
	if repo == nil {
		return nil
	}
	records, err := repo.GetAllSecrets(ctx)
	if err != nil {
		return fmt.Errorf("read encrypted secrets for validation: %w", err)
	}
	if len(records) == 0 {
		return nil
	}
	cipher, err := NewCipher(key)
	if err != nil {
		return err
	}
	for _, rec := range records {
		_, err := cipher.Decrypt(rec.Scope, rec.Owner, rec.Field, rec.Ciphertext)
		if err != nil {
			return ErrKeyMismatch
		}
	}
	return nil
}

// ResolveKey resolves the 32-byte master key according to strict fail-closed and migration policies.
func (m *MasterKeyManager) ResolveKey(ctx context.Context, repo Repository) ([]byte, KeyStatus, error) {
	if m.provider == nil {
		return nil, "", ErrProviderUnavailable
	}

	// 1. Check if key is already present in OS provider
	if m.provider.Available() {
		key, err := m.provider.GetKey(m.targetName)
		if err == nil {
			if len(key) != 32 {
				return nil, "", fmt.Errorf("%w: expected 32 bytes, got %d", ErrCorruptKey, len(key))
			}
			// Cryptographically prove OS key can decrypt existing records
			if err := validateKeyAgainstCiphertext(ctx, repo, key); err != nil {
				return nil, "", fmt.Errorf("%w: OS master key cannot decrypt existing secrets", err)
			}
			return key, KeyStatusLoadedFromOS, nil
		}
		// If error is corruption, fail closed immediately - NEVER overwrite
		if errors.Is(err, ErrCorruptKey) {
			return nil, "", err
		}
		// If error is other than ErrKeyNotFound, report OS provider failure
		if !errors.Is(err, ErrKeyNotFound) {
			return nil, "", fmt.Errorf("read key from provider %q: %w", m.provider.Name(), err)
		}
	}

	// 2. Key not found in OS store (or OS store unavailable). Check legacy environment variable.
	rawEnv := os.Getenv(m.legacyEnv)
	if rawEnv == "" {
		rawEnv = os.Getenv("GODOWNLOADER_MASTER_KEY")
	}

	if rawEnv != "" {
		envKey, err := decodeKey(rawEnv)
		if err != nil {
			return nil, "", fmt.Errorf("invalid environment key: %w", err)
		}

		// Cryptographically prove candidate env key authenticates existing records BEFORE writing to OS
		if err := validateKeyAgainstCiphertext(ctx, repo, envKey); err != nil {
			return nil, "", fmt.Errorf("%w: candidate environment key cannot decrypt existing secrets", err)
		}

		// If OS provider is operational, migrate the key into it
		if m.provider.Available() {
			if setErr := m.provider.SetKey(m.targetName, envKey); setErr != nil {
				return nil, "", fmt.Errorf("write legacy key to OS store: %w", setErr)
			}

			// Read-back verification: prove the OS store wrote and returned exact identical bytes
			readBack, readErr := m.provider.GetKey(m.targetName)
			if readErr != nil {
				return nil, "", fmt.Errorf("read-back verification failed after writing to OS store: %w", readErr)
			}
			if len(readBack) != 32 || !bytes.Equal(envKey, readBack) {
				return nil, "", errors.New("read-back verification mismatch: OS store did not persist identical key bytes")
			}

			// Note on environment cleanup reality:
			// os.Unsetenv only removes the variable from current process memory. It does NOT remove
			// variables configured persistently in the Windows user/system environment or shell profile.
			// Users may safely remove the legacy environment variable from their configuration after migration.
			return envKey, KeyStatusMigratedFromEnv, nil
		}

		// Windows invariant: Windows release-supported runtime must NOT operate in permanent plaintext env fallback.
		if m.provider.Name() == "windows_credential_manager" {
			return nil, "", fmt.Errorf("%w: Windows Credential Manager is unavailable; refusing permanent plaintext environment fallback", ErrProviderUnavailable)
		}

		// On unsupported non-Windows development platforms, allow env fallback for compatibility
		return envKey, KeyStatusEnvFallback, nil
	}

	// 3. No key in OS store and no key in environment.
	// Check if repository already contains encrypted records.
	if repo != nil {
		count, countErr := repo.CountSecrets(ctx)
		if countErr != nil {
			return nil, "", fmt.Errorf("check existing secrets count: %w", countErr)
		}
		if count > 0 {
			// Secrets exist, but key is lost/unprovided: fail closed!
			return nil, "", fmt.Errorf("%w: database contains %d encrypted secrets", ErrKeyMissingForExistingSecrets, count)
		}
	}

	// 4. Empty database (or fresh install) and no key.
	// Generate a fresh 32-byte cryptographic random key and persist to OS provider.
	if !m.provider.Available() {
		return nil, "", ErrProviderUnavailable
	}

	freshKey := make([]byte, 32)
	if _, randErr := io.ReadFull(rand.Reader, freshKey); randErr != nil {
		return nil, "", fmt.Errorf("generate fresh master key: %w", randErr)
	}

	if setErr := m.provider.SetKey(m.targetName, freshKey); setErr != nil {
		return nil, "", fmt.Errorf("persist fresh master key to OS provider: %w", setErr)
	}

	// Read-back verification for fresh key
	readBack, readErr := m.provider.GetKey(m.targetName)
	if readErr != nil {
		return nil, "", fmt.Errorf("read-back verification failed for fresh key: %w", readErr)
	}
	if len(readBack) != 32 || !bytes.Equal(freshKey, readBack) {
		return nil, "", errors.New("read-back verification mismatch: OS store did not persist identical fresh key bytes")
	}

	return freshKey, KeyStatusGeneratedFresh, nil
}

// ResolveCipher resolves the master key and returns an initialized Cipher.
func (m *MasterKeyManager) ResolveCipher(ctx context.Context, repo Repository) (*Cipher, KeyStatus, error) {
	key, status, err := m.ResolveKey(ctx, repo)
	if err != nil {
		return nil, status, err
	}
	cipher, err := NewCipher(key)
	if err != nil {
		return nil, status, err
	}
	return cipher, status, nil
}

// DeleteMasterKey removes the master key from the provider (e.g. for reset/testing).
func (m *MasterKeyManager) DeleteMasterKey() error {
	if m.provider == nil {
		return ErrProviderUnavailable
	}
	return m.provider.DeleteKey(m.targetName)
}
