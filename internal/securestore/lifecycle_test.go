package securestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

// mockSecretRepo implements Repository for lifecycle unit testing.
type mockSecretRepo struct {
	secrets map[string][]byte
}

func newMockSecretRepo() *mockSecretRepo {
	return &mockSecretRepo{secrets: make(map[string][]byte)}
}

func (m *mockSecretRepo) GetSecret(ctx context.Context, scope, owner, field string) ([]byte, error) {
	return m.secrets[scope+"/"+owner+"/"+field], nil
}

func (m *mockSecretRepo) SetSecret(ctx context.Context, scope, owner, field string, ciphertext []byte) error {
	m.secrets[scope+"/"+owner+"/"+field] = ciphertext
	return nil
}

func (m *mockSecretRepo) DeleteSecret(ctx context.Context, scope, owner, field string) error {
	delete(m.secrets, scope+"/"+owner+"/"+field)
	return nil
}

func (m *mockSecretRepo) HasSecret(ctx context.Context, scope, owner, field string) (bool, error) {
	_, ok := m.secrets[scope+"/"+owner+"/"+field]
	return ok, nil
}

func (m *mockSecretRepo) CountSecrets(ctx context.Context) (int, error) {
	return len(m.secrets), nil
}

func (m *mockSecretRepo) GetAllSecrets(ctx context.Context) ([]EncryptedRecord, error) {
	var records []EncryptedRecord
	for k, v := range m.secrets {
		parts := strings.Split(k, "/")
		if len(parts) == 3 {
			records = append(records, EncryptedRecord{
				Scope:      parts[0],
				Owner:      parts[1],
				Field:      parts[2],
				Ciphertext: v,
			})
		}
	}
	return records, nil
}

// helper to generate a random 32-byte key
func generateRandomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		t.Fatal(err)
	}
	return k
}

// Case A: valid-length wrong OS key + existing secrets -> fail closed
func TestLifecycle_CaseA_ValidLengthWrongOSKeyFailsClosed(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()

	keyA := generateRandomKey(t)
	keyB := generateRandomKey(t)

	// Encrypt a secret with Key A
	cipherA, err := NewCipher(keyA)
	if err != nil {
		t.Fatal(err)
	}
	storeA := NewStore(repo, cipherA)
	if err := storeA.Put(context.Background(), "settings", "global", "proxy_password", "my-proxy-secret"); err != nil {
		t.Fatal(err)
	}

	// Store Key B in the OS provider (len 32, but B != A)
	if err := provider.SetKey("test-target", keyB); err != nil {
		t.Fatal(err)
	}

	mgr := NewMasterKeyManager(provider, "test-target", "NON_EXISTENT_VAR")

	// Must fail closed with ErrKeyMismatch
	_, _, err = mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error on mismatching OS key, got nil")
	}
	if !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("expected ErrKeyMismatch, got: %v", err)
	}

	// Invariant: Key B must NOT be overwritten, DB secrets untouched, no new key generated
	storedKey, _ := provider.GetKey("test-target")
	if !bytes.Equal(storedKey, keyB) {
		t.Fatal("stored OS key must remain untouched on mismatch")
	}
	count, _ := repo.CountSecrets(context.Background())
	if count != 1 {
		t.Fatalf("database secret count must remain 1, got %d", count)
	}
}

// Case B: valid-length wrong env key + existing secrets -> not written to OS
func TestLifecycle_CaseB_ValidLengthWrongEnvKeyNotWrittenToOS(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()

	keyA := generateRandomKey(t)
	keyB := generateRandomKey(t)

	// Encrypt a secret with Key A
	cipherA, _ := NewCipher(keyA)
	storeA := NewStore(repo, cipherA)
	_ = storeA.Put(context.Background(), "settings", "global", "proxy_password", "my-proxy-secret")

	// Set Key B in environment variable
	t.Setenv("WRONG_ENV_KEY", hex.EncodeToString(keyB))

	mgr := NewMasterKeyManager(provider, "test-target", "WRONG_ENV_KEY")

	// Must fail closed
	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error on candidate env key mismatch, got nil")
	}
	if !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("expected ErrKeyMismatch, got: %v", err)
	}

	// Invariant: Key B must NOT be written into OS provider!
	_, err = provider.GetKey("test-target")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("OS provider must remain empty when env key does not match ciphertext! got err: %v", err)
	}
}

// Case C: correct env key + existing secrets -> authenticated, written, read back, migrated
func TestLifecycle_CaseC_CorrectEnvKeyAuthenticatedAndMigrated(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()

	keyA := generateRandomKey(t)

	// Encrypt a secret with Key A
	cipherA, _ := NewCipher(keyA)
	storeA := NewStore(repo, cipherA)
	_ = storeA.Put(context.Background(), "mediaauth", "default", "cookies.txt", "cookie-content-xyz")

	// Set Key A in environment variable
	t.Setenv("CORRECT_ENV_KEY", hex.EncodeToString(keyA))

	mgr := NewMasterKeyManager(provider, "test-target", "CORRECT_ENV_KEY")

	// Must succeed with KeyStatusMigratedFromEnv
	key, status, err := mgr.ResolveKey(context.Background(), repo)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if status != KeyStatusMigratedFromEnv {
		t.Fatalf("expected status %s, got %s", KeyStatusMigratedFromEnv, status)
	}
	if !bytes.Equal(key, keyA) {
		t.Fatal("migrated key bytes do not match original key")
	}

	// Invariant: OS provider now contains Key A
	storedKey, err := provider.GetKey("test-target")
	if err != nil || !bytes.Equal(storedKey, keyA) {
		t.Fatalf("expected Key A in OS provider, got %v", err)
	}

	// Subsequent boot reads from OS
	mgr2 := NewMasterKeyManager(provider, "test-target", "CORRECT_ENV_KEY")
	key2, status2, err := mgr2.ResolveKey(context.Background(), repo)
	if err != nil || status2 != KeyStatusLoadedFromOS || !bytes.Equal(key2, keyA) {
		t.Fatalf("subsequent boot failed: %v, status=%s", err, status2)
	}
}

// Case D: OS Write failure -> legacy source preserved, fail closed
func TestLifecycle_CaseD_OSWriteFailureFailsClosed(t *testing.T) {
	provider := NewMemoryKeyProvider()
	provider.SetFailSetKey(errors.New("simulated Win32 disk write error"))
	repo := newMockSecretRepo()

	keyA := generateRandomKey(t)
	cipherA, _ := NewCipher(keyA)
	storeA := NewStore(repo, cipherA)
	_ = storeA.Put(context.Background(), "settings", "global", "proxy_password", "test")

	t.Setenv("WRITE_FAIL_ENV_KEY", hex.EncodeToString(keyA))
	mgr := NewMasterKeyManager(provider, "test-target", "WRITE_FAIL_ENV_KEY")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error on OS write failure, got nil")
	}
	if !strings.Contains(err.Error(), "write legacy key to OS store") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Invariant: no key in provider, ciphertext untouched
	_, err = provider.GetKey("test-target")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("provider must remain empty after write failure, got %v", err)
	}
}

// Case E: OS read-back failure -> migration not accepted
func TestLifecycle_CaseE_OSReadBackFailureDoesNotAcceptMigration(t *testing.T) {
	provider := NewMemoryKeyProvider()
	provider.SetFailGetKeyAfterWrite(errors.New("simulated read-back hardware fault"))
	repo := newMockSecretRepo()

	keyA := generateRandomKey(t)
	cipherA, _ := NewCipher(keyA)
	storeA := NewStore(repo, cipherA)
	_ = storeA.Put(context.Background(), "settings", "global", "proxy_password", "test")

	t.Setenv("READBACK_FAIL_ENV_KEY", hex.EncodeToString(keyA))
	mgr := NewMasterKeyManager(provider, "test-target", "READBACK_FAIL_ENV_KEY")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error on read-back failure, got nil")
	}
	if !strings.Contains(err.Error(), "read-back verification failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// Case F: OS read-back different bytes -> migration not accepted
func TestLifecycle_CaseF_OSReadBackDifferentBytesDoesNotAcceptMigration(t *testing.T) {
	provider := NewMemoryKeyProvider()
	differentBytes := generateRandomKey(t)
	provider.SetDifferentReadBytesAfterWrite(differentBytes)
	repo := newMockSecretRepo()

	keyA := generateRandomKey(t)
	cipherA, _ := NewCipher(keyA)
	storeA := NewStore(repo, cipherA)
	_ = storeA.Put(context.Background(), "settings", "global", "proxy_password", "test")

	t.Setenv("DIFF_READBACK_ENV_KEY", hex.EncodeToString(keyA))
	mgr := NewMasterKeyManager(provider, "test-target", "DIFF_READBACK_ENV_KEY")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error on read-back byte mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "read-back verification mismatch") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// Case G: fresh secret DB + valid stale/existing OS key -> defined safe behavior
func TestLifecycle_CaseG_FreshDBReusesExistingValidOSKey(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo() // 0 secrets

	keyExisting := generateRandomKey(t)
	_ = provider.SetKey("test-target", keyExisting)

	mgr := NewMasterKeyManager(provider, "test-target", "UNSET_VAR")

	// Reuses the existing valid OS key
	key, status, err := mgr.ResolveKey(context.Background(), repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != KeyStatusLoadedFromOS {
		t.Fatalf("expected loaded_from_os, got %s", status)
	}
	if !bytes.Equal(key, keyExisting) {
		t.Fatal("did not reuse existing OS key")
	}
}

// Case H: existing encrypted rows + no usable key -> fail-closed behavior retained
func TestLifecycle_CaseH_ExistingSecretsWithoutKeyFailsClosed(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()

	_ = repo.SetSecret(context.Background(), "settings", "global", "proxy_password", []byte("v1-existing-cipher"))

	mgr := NewMasterKeyManager(provider, "test-target", "UNSET_VAR")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error when secrets exist without key, got nil")
	}
	if !errors.Is(err, ErrKeyMissingForExistingSecrets) {
		t.Fatalf("expected ErrKeyMissingForExistingSecrets, got: %v", err)
	}
	// Refuses to generate key
	_, err = provider.GetKey("test-target")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("must not generate key into provider, got %v", err)
	}
}

// Case I: malformed OS key -> fail-closed behavior retained
func TestLifecycle_CaseI_MalformedOSKeyFailsClosed(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()

	provider.SetRawKey("test-target", []byte("too-short-16byte"))
	mgr := NewMasterKeyManager(provider, "test-target", "UNSET_VAR")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("expected error on malformed key, got nil")
	}
	if !errors.Is(err, ErrCorruptKey) {
		t.Fatalf("expected ErrCorruptKey, got: %v", err)
	}
}

// Windows Environment Fallback Refusal: Windows runtime refuses permanent plaintext env fallback
type winMockProvider struct {
	*MemoryKeyProvider
}

func (w *winMockProvider) Name() string {
	return "windows_credential_manager"
}

func (w *winMockProvider) Available() bool {
	return false // simulate Credential Manager unavailable
}

func TestLifecycle_WindowsRefusesEnvFallbackWhenOSUnavailable(t *testing.T) {
	provider := &winMockProvider{MemoryKeyProvider: NewMemoryKeyProvider()}
	repo := newMockSecretRepo()

	key := generateRandomKey(t)
	t.Setenv("WIN_REFUSE_ENV_KEY", hex.EncodeToString(key))
	mgr := NewMasterKeyManager(provider, "test-target", "WIN_REFUSE_ENV_KEY")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err == nil {
		t.Fatal("Windows must refuse permanent plaintext env fallback when OS store is unavailable")
	}
	if !strings.Contains(err.Error(), "refusing permanent plaintext environment fallback") {
		t.Fatalf("expected refusal error, got: %v", err)
	}
}

// Fresh install generates fresh key and verifies read-back
func TestLifecycle_FreshInstallGeneratesKey(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()
	mgr := NewMasterKeyManager(provider, "test-target", "NON_EXISTENT_ENV_VAR")

	cipher, status, err := mgr.ResolveCipher(context.Background(), repo)
	if err != nil {
		t.Fatalf("unexpected error on fresh install: %v", err)
	}
	if status != KeyStatusGeneratedFresh {
		t.Fatalf("expected status %s, got %s", KeyStatusGeneratedFresh, status)
	}
	if !cipher.Available() {
		t.Fatal("cipher should be available after key generation")
	}

	savedKey, err := provider.GetKey("test-target")
	if err != nil || len(savedKey) != 32 {
		t.Fatalf("key should be 32 bytes, got %v", err)
	}
}

// Delete key removes it from provider
func TestLifecycle_DeleteMasterKey(t *testing.T) {
	provider := NewMemoryKeyProvider()
	repo := newMockSecretRepo()
	mgr := NewMasterKeyManager(provider, "test-target", "UNSET_VAR_DEL")

	_, _, err := mgr.ResolveKey(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.DeleteMasterKey(); err != nil {
		t.Fatalf("failed to delete master key: %v", err)
	}

	_, err = provider.GetKey("test-target")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound after deletion, got %v", err)
	}
}
