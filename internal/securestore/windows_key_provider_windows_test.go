//go:build windows

package securestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

func TestWindowsCredentialManager_WriteReadDeleteCycle(t *testing.T) {
	provider := NewWindowsCredentialManagerProvider()
	if !provider.Available() {
		t.Skip("Windows Credential Manager is not available on this host")
	}

	target := "GoDownloader_Test_Cycle_" + uuid.NewString()
	t.Cleanup(func() {
		_ = provider.DeleteKey(target)
	})

	// Key should not exist initially
	_, err := provider.GetKey(target)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound for non-existent key, got: %v", err)
	}

	// Generate and write 32-byte key
	testKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, testKey); err != nil {
		t.Fatal(err)
	}

	if err := provider.SetKey(target, testKey); err != nil {
		t.Fatalf("failed to write key to Windows Credential Manager: %v", err)
	}

	// Read back and assert equality
	readKey, err := provider.GetKey(target)
	if err != nil {
		t.Fatalf("failed to read key from Windows Credential Manager: %v", err)
	}
	if !bytes.Equal(testKey, readKey) {
		t.Fatal("read key bytes do not match written key bytes")
	}

	// Delete key
	if err := provider.DeleteKey(target); err != nil {
		t.Fatalf("failed to delete key: %v", err)
	}

	// Verify key is gone
	_, err = provider.GetKey(target)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound after deletion, got: %v", err)
	}

	// Idempotent delete
	if err := provider.DeleteKey(target); err != nil {
		t.Fatalf("idempotent delete failed: %v", err)
	}
}

func TestWindowsCredentialManager_NonExistentKeyReturnsKeyNotFound(t *testing.T) {
	provider := NewWindowsCredentialManagerProvider()
	if !provider.Available() {
		t.Skip("Windows Credential Manager is not available on this host")
	}

	target := "GoDownloader_Test_Missing_" + uuid.NewString()
	_, err := provider.GetKey(target)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got: %v", err)
	}
}

func TestWindowsCredentialManager_CorruptKeyReturnsErrCorruptKey(t *testing.T) {
	provider := NewWindowsCredentialManagerProvider()
	if !provider.Available() {
		t.Skip("Windows Credential Manager is not available on this host")
	}

	target := "GoDownloader_Test_Corrupt_" + uuid.NewString()
	t.Cleanup(func() {
		_ = provider.DeleteKey(target)
	})

	// Directly write an invalid 12-byte blob to Credential Manager via Win32 API
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	shortBlob := []byte("short-12byte")
	commentPtr, _ := windows.UTF16PtrFromString("Test Corrupt Key")

	cred := winCredential{
		Flags:              0,
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		Comment:            commentPtr,
		CredentialBlobSize: uint32(len(shortBlob)),
		CredentialBlob:     &shortBlob[0],
		Persist:            credPersistLocalMachine,
	}

	r, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		t.Fatalf("failed to write raw credential: %v", callErr)
	}

	// GetKey must detect wrong size and return ErrCorruptKey
	_, err = provider.GetKey(target)
	if !errors.Is(err, ErrCorruptKey) {
		t.Fatalf("expected ErrCorruptKey on short credential blob, got: %v", err)
	}
}

func TestWindowsCredentialManager_EndToEndLifecycleIntegration(t *testing.T) {
	provider := NewWindowsCredentialManagerProvider()
	if !provider.Available() {
		t.Skip("Windows Credential Manager is not available on this host")
	}

	target := "GoDownloader_Test_E2E_" + uuid.NewString()
	t.Cleanup(func() {
		_ = provider.DeleteKey(target)
	})

	repo := newMockSecretRepo()
	mgr := NewMasterKeyManager(provider, target, "UNSET_VAR_WIN_E2E")

	// 1. Fresh generation in real Windows Credential Manager
	cipher1, status1, err := mgr.ResolveCipher(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolve cipher fresh failed: %v", err)
	}
	if status1 != KeyStatusGeneratedFresh {
		t.Fatalf("expected status generated_fresh, got: %s", status1)
	}

	// Store a secret using real cipher
	store1 := NewStore(repo, cipher1)
	if err := store1.Put(context.Background(), "settings", "global", "proxy_password", "real-win-secret-999"); err != nil {
		t.Fatalf("store put failed: %v", err)
	}

	// 2. Restart simulation: create a new MasterKeyManager pointing to the same Windows Credential Manager target
	mgr2 := NewMasterKeyManager(provider, target, "UNSET_VAR_WIN_E2E")
	cipher2, status2, err := mgr2.ResolveCipher(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolve cipher loaded failed: %v", err)
	}
	if status2 != KeyStatusLoadedFromOS {
		t.Fatalf("expected status loaded_from_os, got: %s", status2)
	}

	store2 := NewStore(repo, cipher2)
	val, err := store2.Get(context.Background(), "settings", "global", "proxy_password")
	if err != nil {
		t.Fatalf("store get failed: %v", err)
	}
	if val != "real-win-secret-999" {
		t.Fatalf("expected %q, got %q", "real-win-secret-999", val)
	}
}
