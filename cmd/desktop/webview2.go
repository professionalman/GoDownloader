package main

import (
	"strconv"
	"strings"
)

// WebView2 Runtime detection follows Microsoft's documented approach ("Detect if a WebView2 Runtime is already
// installed", Distribute your app and the WebView2 Runtime): read the `pv` (REG_SZ) value of the Evergreen
// Runtime's EdgeUpdate client key, per-machine (HKLM, 32-bit registry view — WOW6432Node on 64-bit Windows) and
// per-user (HKCU). The Runtime is installed if at least one location holds a version greater than 0.0.0.0; a
// missing key, an empty value, or 0.0.0.0 means "not installed". No minimum version is asserted: the Evergreen
// Runtime updates itself, and GoDownloader does not check a feature level.

// webView2ClientKey is the Evergreen WebView2 Runtime client key (relative to HKLM\SOFTWARE or HKCU\Software).
const webView2ClientKey = `Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// webView2Probe is one registry location's observation.
type webView2Probe struct {
	Scope   string // "per-machine" or "per-user"
	Found   bool   // the `pv` value could be read
	Version string // raw `pv` value
}

// webView2Status is the evaluated detection result.
type webView2Status struct {
	Installed bool
	Version   string
	Scope     string
}

// validWebView2Version reports whether v is a dotted numeric version greater than 0.0.0.0.
func validWebView2Version(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	nonZero := false
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		if n > 0 {
			nonZero = true
		}
	}
	return nonZero
}

// evaluateWebView2 applies Microsoft's rule to the probed locations: installed if any location holds a valid
// version. The first valid location (per-machine before per-user) is reported.
func evaluateWebView2(probes []webView2Probe) webView2Status {
	for _, p := range probes {
		if p.Found && validWebView2Version(p.Version) {
			return webView2Status{Installed: true, Version: strings.TrimSpace(p.Version), Scope: p.Scope}
		}
	}
	return webView2Status{}
}

// webView2MissingMessage is shown before any window or background runtime starts when the Runtime is absent.
const webView2MissingMessage = "GoDownloader needs the Microsoft Edge WebView2 Runtime to display its window, " +
	"but the Runtime was not found on this computer.\n\n" +
	"Install the Evergreen WebView2 Runtime from Microsoft " +
	"(https://developer.microsoft.com/microsoft-edge/webview2/), then start GoDownloader again.\n\n" +
	"GoDownloader has not started."

// webView2StartupDecision decides whether the desktop app may start its window and background runtime. A missing
// Runtime stops startup before the core starts, so the app never runs as a headless tray process that looks
// healthy but cannot show its interface.
func webView2StartupDecision(st webView2Status) (proceed bool, message string) {
	if st.Installed {
		return true, ""
	}
	return false, webView2MissingMessage
}
