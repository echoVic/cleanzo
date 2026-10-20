package main

import (
	"strings"
	"testing"
)

// TestWebViewPrompt checks the native fallback before a page can translate it.
func TestWebViewPrompt(t *testing.T) {
	for _, locale := range []string{"en", "zh-Hans"} {
		opts := webViewPrompt(locale)
		if len(opts.Buttons) != 2 || opts.CancelButton != 1 || opts.DefaultButton != 0 {
			t.Fatalf("%s: unusable prompt: %+v", locale, opts)
		}
		if !strings.Contains(opts.Message, "WebView2") || !strings.Contains(opts.Detail, webViewDownloadURL) {
			t.Fatalf("%s: missing runtime or download URL: %+v", locale, opts)
		}
		if locale == "en" && opts.Buttons[0] != "Download" || locale == "zh-Hans" && opts.Buttons[0] != "下载" {
			t.Fatalf("%s: wrong language: %q", locale, opts.Buttons)
		}
	}
}
