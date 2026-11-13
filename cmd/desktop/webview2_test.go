package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidWebView2Version(t *testing.T) {
	cases := map[string]bool{
		"130.0.2849.80":  true,
		" 86.0.616.0 ":   true,
		"1":              true,
		"0.0.0.1":        true,
		"":               false,
		"   ":            false,
		"0.0.0.0":        false, // documented "not installed" sentinel
		"0":              false,
		"abc":            false,
		"1..2":           false,
		"1.2.x.4":        false,
		"-1.0.0.0":       false,
		"99999999999.0.": false,
	}
	for v, want := range cases {
		if got := validWebView2Version(v); got != want {
			t.Errorf("validWebView2Version(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestEvaluateWebView2_MicrosoftRule(t *testing.T) {
	machine := func(found bool, v string) webView2Probe {
		return webView2Probe{Scope: "per-machine", Found: found, Version: v}
	}
	user := func(found bool, v string) webView2Probe {
		return webView2Probe{Scope: "per-user", Found: found, Version: v}
	}

	cases := []struct {
		name      string
		probes    []webView2Probe
		installed bool
		scope     string
	}{
		{"neither key exists", []webView2Probe{machine(false, ""), user(false, "")}, false, ""},
		{"per-machine only", []webView2Probe{machine(true, "130.0.2849.80"), user(false, "")}, true, "per-machine"},
		{"per-user only (per-user install)", []webView2Probe{machine(false, ""), user(true, "130.0.2849.80")}, true, "per-user"},
		{"one key empty, other missing", []webView2Probe{machine(true, ""), user(false, "")}, false, ""},
		{"one key 0.0.0.0, other missing", []webView2Probe{machine(false, ""), user(true, "0.0.0.0")}, false, ""},
		{"both invalid", []webView2Probe{machine(true, "0.0.0.0"), user(true, "")}, false, ""},
		{"machine invalid, user valid", []webView2Probe{machine(true, "0.0.0.0"), user(true, "129.0.1.2")}, true, "per-user"},
		{"garbage value rejected", []webView2Probe{machine(true, "installed"), user(false, "")}, false, ""},
	}
	for _, c := range cases {
		st := evaluateWebView2(c.probes)
		if st.Installed != c.installed || st.Scope != c.scope {
			t.Errorf("%s: got installed=%v scope=%q, want installed=%v scope=%q", c.name, st.Installed, st.Scope, c.installed, c.scope)
		}
	}
}

func TestWebView2StartupDecision(t *testing.T) {
	if ok, msg := webView2StartupDecision(webView2Status{Installed: true, Version: "130.0.0.1"}); !ok || msg != "" {
		t.Fatalf("installed runtime must proceed silently: %v %q", ok, msg)
	}
	ok, msg := webView2StartupDecision(webView2Status{})
	if ok {
		t.Fatal("missing runtime must not proceed")
	}
	for _, want := range []string{"WebView2 Runtime", "not found", "https://developer.microsoft.com/microsoft-edge/webview2/", "has not started"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
	// No unverified minimum-version claim.
	if strings.Contains(msg, "or higher") || strings.Contains(msg, "86.0") {
		t.Errorf("message claims a minimum version that is not checked: %s", msg)
	}
}

// The installer applies the same documented rule as the desktop host: the EdgeUpdate *Clients* key's `pv` value,
// HKLM through the 32-bit view and HKCU, rejecting empty and 0.0.0.0, with no unverified minimum-version claim.
func TestInstallerWebView2Detection_MatchesDocumentedRule(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "build", "windows", "installer.nsi"))
	if err != nil {
		t.Fatal(err)
	}
	nsi := string(data)
	start := strings.Index(nsi, "Function CheckWebView2")
	end := strings.Index(nsi[start:], "FunctionEnd")
	if start < 0 || end < 0 {
		t.Fatal("CheckWebView2 not found")
	}
	fn := nsi[start : start+end]
	for _, want := range []string{
		`SetRegView 32`,
		`ReadRegStr $1 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"`,
		`ReadRegStr $1 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"`,
		`${AndIf} $1 != "0.0.0.0"`,
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("installer detection missing %s", want)
		}
	}
	for _, forbidden := range []string{"ClientState", "EBWebView", `Uninstall\Microsoft Edge WebView2`} {
		if strings.Contains(fn, forbidden) {
			t.Errorf("installer detection still uses undocumented %s", forbidden)
		}
	}
	if strings.Contains(nsi, "86.0.616.0") || strings.Contains(nsi, "or higher) is required") {
		t.Error("installer claims a minimum WebView2 version it does not check")
	}
}
