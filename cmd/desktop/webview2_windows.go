//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// readWebView2Probe reads the `pv` value of keyPath under root with the given extra access flags (for example
// registry.WOW64_32KEY). Only a read is performed.
func readWebView2Probe(scope string, root registry.Key, keyPath string, viewFlags uint32) webView2Probe {
	k, err := registry.OpenKey(root, keyPath, registry.QUERY_VALUE|viewFlags)
	if err != nil {
		return webView2Probe{Scope: scope}
	}
	defer k.Close()
	v, _, err := k.GetStringValue("pv")
	if err != nil {
		return webView2Probe{Scope: scope}
	}
	return webView2Probe{Scope: scope, Found: true, Version: v}
}

// detectWebView2 probes Microsoft's documented locations. The per-machine key is read through the 32-bit registry
// view: on 64-bit Windows that is HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{…}, on 32-bit Windows
// (where the flag is ignored) HKLM\SOFTWARE\Microsoft\EdgeUpdate\Clients\{…} — exactly the two documented paths.
func detectWebView2() webView2Status {
	return evaluateWebView2([]webView2Probe{
		readWebView2Probe("per-machine", registry.LOCAL_MACHINE, webView2ClientKey, registry.WOW64_32KEY),
		readWebView2Probe("per-user", registry.CURRENT_USER, webView2ClientKey, 0),
	})
}

// showWebView2MissingError reports the missing Runtime with a native dialog (no webview required).
func showWebView2MissingError(message string) {
	title, _ := windows.UTF16PtrFromString("GoDownloader — WebView2 Runtime required")
	msg, _ := windows.UTF16PtrFromString(message)
	windows.MessageBox(0, msg, title, windows.MB_OK|windows.MB_ICONERROR)
}
