//go:build windows

package main

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The reader returns the `pv` REG_SZ value of the given key and reports a missing key or value as not found. A
// throwaway key under HKCU\Software\GoDownloaderTest is created for the test and deleted afterwards.
func TestReadWebView2Probe_RegistryValue(t *testing.T) {
	parent := `Software\GoDownloaderTest`
	keyPath := fmt.Sprintf(`%s\WebView2Probe-%d`, parent, os.Getpid())
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		t.Skipf("cannot create test registry key: %v", err)
	}
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
		_ = registry.DeleteKey(registry.CURRENT_USER, parent) // only succeeds if empty
	})

	if p := readWebView2Probe("per-user", registry.CURRENT_USER, keyPath, 0); p.Found {
		t.Fatalf("key without pv must not be found: %+v", p)
	}
	if err := k.SetStringValue("pv", "0.0.0.0"); err != nil {
		t.Fatal(err)
	}
	if st := evaluateWebView2([]webView2Probe{readWebView2Probe("per-user", registry.CURRENT_USER, keyPath, 0)}); st.Installed {
		t.Fatal("pv=0.0.0.0 must mean not installed")
	}
	if err := k.SetStringValue("pv", "131.0.2903.70"); err != nil {
		t.Fatal(err)
	}
	k.Close()
	st := evaluateWebView2([]webView2Probe{readWebView2Probe("per-user", registry.CURRENT_USER, keyPath, 0)})
	if !st.Installed || st.Version != "131.0.2903.70" || st.Scope != "per-user" {
		t.Fatalf("valid pv not detected: %+v", st)
	}
	if p := readWebView2Probe("per-user", registry.CURRENT_USER, keyPath+`\missing`, 0); p.Found {
		t.Fatalf("missing key must not be found: %+v", p)
	}
}

// Records what the documented detection reports on this machine (informational: development machines running
// Wails have the Runtime; a machine without it is covered by the clean-VM gap in the readiness report).
func TestDetectWebView2_ThisMachine(t *testing.T) {
	st := detectWebView2()
	t.Logf("WebView2 detection on this machine: installed=%v version=%q scope=%q", st.Installed, st.Version, st.Scope)
}
