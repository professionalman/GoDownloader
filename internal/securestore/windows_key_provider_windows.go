//go:build windows

package securestore

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credTypeGeneric         uint32 = 1
	credPersistLocalMachine uint32 = 2
)

type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

// WindowsCredentialManagerProvider stores master keys in the Windows Credential Manager.
type WindowsCredentialManagerProvider struct{}

// NewWindowsCredentialManagerProvider creates a Windows Credential Manager key provider.
func NewWindowsCredentialManagerProvider() *WindowsCredentialManagerProvider {
	return &WindowsCredentialManagerProvider{}
}

func (w *WindowsCredentialManagerProvider) Name() string {
	return "windows_credential_manager"
}

func (w *WindowsCredentialManagerProvider) Available() bool {
	return advapi32.Load() == nil
}

func (w *WindowsCredentialManagerProvider) GetKey(target string) ([]byte, error) {
	if !w.Available() {
		return nil, ErrProviderUnavailable
	}
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target name %q: %w", target, err)
	}

	var pCred *winCredential
	r, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&pCred)),
	)
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("read Windows credential %q: %w", target, callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pCred)))

	if pCred.CredentialBlobSize != 32 || pCred.CredentialBlob == nil {
		return nil, ErrCorruptKey
	}

	raw := unsafe.Slice(pCred.CredentialBlob, pCred.CredentialBlobSize)
	key := make([]byte, len(raw))
	copy(key, raw)
	return key, nil
}

func (w *WindowsCredentialManagerProvider) SetKey(target string, key []byte) error {
	if !w.Available() {
		return ErrProviderUnavailable
	}
	if len(key) != 32 {
		return errors.New("master key must be exactly 32 bytes")
	}

	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("invalid target name %q: %w", target, err)
	}
	commentPtr, _ := windows.UTF16PtrFromString("GoDownloader Master Key")

	cred := winCredential{
		Flags:              0,
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		Comment:            commentPtr,
		CredentialBlobSize: uint32(len(key)),
		CredentialBlob:     &key[0],
		Persist:            credPersistLocalMachine,
	}

	r, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		return fmt.Errorf("write Windows credential %q: %w", target, callErr)
	}
	return nil
}

func (w *WindowsCredentialManagerProvider) DeleteKey(target string) error {
	if !w.Available() {
		return ErrProviderUnavailable
	}
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("invalid target name %q: %w", target, err)
	}

	r, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(targetPtr)), uintptr(credTypeGeneric), 0)
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil // idempotent deletion
		}
		return fmt.Errorf("delete Windows credential %q: %w", target, callErr)
	}
	return nil
}

// NewDefaultKeyProvider returns the platform-standard OS key provider.
func NewDefaultKeyProvider() KeyProvider {
	return NewWindowsCredentialManagerProvider()
}
