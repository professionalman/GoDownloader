package securestore

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

var (
	// ErrKeyNotFound indicates the requested key target does not exist in the provider.
	ErrKeyNotFound = errors.New("master key not found in provider")

	// ErrCorruptKey indicates the key was found but has an invalid length or is malformed.
	ErrCorruptKey = errors.New("master key in provider is corrupt or invalid length")

	// ErrProviderUnavailable indicates the key provider cannot be reached or is unsupported.
	ErrProviderUnavailable = errors.New("key provider is unavailable on this platform/session")

	// ErrKeyMissingForExistingSecrets indicates secrets exist in storage, but no key is present.
	// Generating a new random key in this state would cause permanent data corruption.
	ErrKeyMissingForExistingSecrets = errors.New("encrypted secrets exist in database, but master key is missing; refusing to generate new key")

	// ErrKeyMismatch indicates candidate master key cannot authenticate existing encrypted secrets.
	ErrKeyMismatch = errors.New("master key does not authenticate existing encrypted secrets")
)

// KeyProvider is the contract for an OS-protected or test key storage backend.
type KeyProvider interface {
	// Name returns the identifier of the provider backend (e.g. "windows_credential_manager", "memory").
	Name() string

	// Available returns true if the provider backend is operational in the current environment.
	Available() bool

	// GetKey retrieves the raw 32-byte key for the given target name.
	// Returns ErrKeyNotFound if no key exists.
	// Returns ErrCorruptKey if the stored data is not exactly 32 bytes.
	GetKey(target string) ([]byte, error)

	// SetKey stores the 32-byte key under the given target name.
	SetKey(target string, key []byte) error

	// DeleteKey removes the key under the given target name.
	DeleteKey(target string) error
}

// MemoryKeyProvider is a thread-safe, in-memory implementation of KeyProvider for testing.
type MemoryKeyProvider struct {
	mu                      sync.RWMutex
	keys                    map[string][]byte
	available               bool
	failSetKey              error
	failGetKey              error
	failGetKeyAfterWrite    error
	diffReadBytes           []byte
	diffReadBytesAfterWrite []byte
	written                 bool
}

// NewMemoryKeyProvider creates an active in-memory key provider.
func NewMemoryKeyProvider() *MemoryKeyProvider {
	return &MemoryKeyProvider{
		keys:      make(map[string][]byte),
		available: true,
	}
}

func (m *MemoryKeyProvider) Name() string {
	return "memory"
}

func (m *MemoryKeyProvider) Available() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.available
}

// SetAvailable toggles availability to simulate OS store outages in tests.
func (m *MemoryKeyProvider) SetAvailable(avail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.available = avail
}

// SetFailSetKey configures SetKey to return a simulated write error.
func (m *MemoryKeyProvider) SetFailSetKey(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failSetKey = err
}

// SetFailGetKey configures GetKey to return a simulated read error.
func (m *MemoryKeyProvider) SetFailGetKey(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failGetKey = err
}

// SetFailGetKeyAfterWrite configures GetKey to return a simulated error only on read-back after SetKey.
func (m *MemoryKeyProvider) SetFailGetKeyAfterWrite(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failGetKeyAfterWrite = err
}

// SetDifferentReadBytes configures GetKey to return different bytes to test read-back verification.
func (m *MemoryKeyProvider) SetDifferentReadBytes(b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diffReadBytes = b
}

// SetDifferentReadBytesAfterWrite configures GetKey to return different bytes only on read-back after SetKey.
func (m *MemoryKeyProvider) SetDifferentReadBytesAfterWrite(b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diffReadBytesAfterWrite = b
}

func (m *MemoryKeyProvider) GetKey(target string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.available {
		return nil, ErrProviderUnavailable
	}
	if m.written && m.failGetKeyAfterWrite != nil {
		return nil, m.failGetKeyAfterWrite
	}
	if m.written && m.diffReadBytesAfterWrite != nil {
		cp := make([]byte, len(m.diffReadBytesAfterWrite))
		copy(cp, m.diffReadBytesAfterWrite)
		return cp, nil
	}
	if m.failGetKey != nil {
		return nil, m.failGetKey
	}
	if m.diffReadBytes != nil {
		cp := make([]byte, len(m.diffReadBytes))
		copy(cp, m.diffReadBytes)
		return cp, nil
	}
	val, ok := m.keys[target]
	if !ok {
		return nil, ErrKeyNotFound
	}
	if len(val) != 32 {
		return nil, ErrCorruptKey
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	return cp, nil
}

func (m *MemoryKeyProvider) SetKey(target string, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.available {
		return ErrProviderUnavailable
	}
	if m.failSetKey != nil {
		return m.failSetKey
	}
	if len(key) != 32 {
		return errors.New("key must be exactly 32 bytes")
	}
	cp := make([]byte, len(key))
	copy(cp, key)
	m.keys[target] = cp
	m.written = true
	return nil
}

// SetRawKey allows tests to store malformed keys to verify corruption detection.
func (m *MemoryKeyProvider) SetRawKey(target string, raw []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(raw))
	copy(cp, raw)
	m.keys[target] = cp
}

func (m *MemoryKeyProvider) DeleteKey(target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.available {
		return ErrProviderUnavailable
	}
	delete(m.keys, target)
	return nil
}

// EnvKeyProvider provides keys from environment variables.
type EnvKeyProvider struct {
	envVar string
}

// NewEnvKeyProvider creates a provider backed by the specified environment variable name.
func NewEnvKeyProvider(envVar string) *EnvKeyProvider {
	if envVar == "" {
		envVar = "V0.7_SETTINGS_ENCRYPTION_KEY"
	}
	return &EnvKeyProvider{envVar: envVar}
}

func (e *EnvKeyProvider) Name() string {
	return "environment"
}

func (e *EnvKeyProvider) Available() bool {
	return os.Getenv(e.envVar) != ""
}

func (e *EnvKeyProvider) GetKey(target string) ([]byte, error) {
	raw := os.Getenv(e.envVar)
	if raw == "" {
		return nil, ErrKeyNotFound
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptKey, err)
	}
	return key, nil
}

func (e *EnvKeyProvider) SetKey(target string, key []byte) error {
	if len(key) != 32 {
		return errors.New("key must be exactly 32 bytes")
	}
	return os.Setenv(e.envVar, fmt.Sprintf("%x", key))
}

func (e *EnvKeyProvider) DeleteKey(target string) error {
	return os.Unsetenv(e.envVar)
}
