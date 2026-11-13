//go:build !windows

package main

import (
	"fmt"
	"os"
)

// detectWebView2 is Windows-only; other platforms use their native webview and are not gated here.
func detectWebView2() webView2Status {
	return webView2Status{Installed: true, Scope: "not-applicable"}
}

func showWebView2MissingError(message string) {
	fmt.Fprintln(os.Stderr, message)
}
