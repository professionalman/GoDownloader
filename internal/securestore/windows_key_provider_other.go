//go:build !windows

package securestore

// NonWindowsKeyProvider is a stub provider on non-Windows platforms where OS credential manager is unavailable.
type NonWindowsKeyProvider struct{}

func (n *NonWindowsKeyProvider) Name() string {
	return "unsupported_os"
}

func (n *NonWindowsKeyProvider) Available() bool {
	return false
}

func (n *NonWindowsKeyProvider) GetKey(target string) ([]byte, error) {
	return nil, ErrProviderUnavailable
}

func (n *NonWindowsKeyProvider) SetKey(target string, key []byte) error {
	return ErrProviderUnavailable
}

func (n *NonWindowsKeyProvider) DeleteKey(target string) error {
	return ErrProviderUnavailable
}

// NewDefaultKeyProvider returns a stub key provider on non-Windows platforms.
func NewDefaultKeyProvider() KeyProvider {
	return &NonWindowsKeyProvider{}
}
