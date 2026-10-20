package main

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/egoist/mygo"
)

// webViewDownloadURL is the official runtime download page.
const webViewDownloadURL = "https://developer.microsoft.com/microsoft-edge/webview2/"

// nativeMessages shares the page's translations with the no-webview prompt.
//
//go:embed src/native-i18n.json
var nativeMessages []byte

// webViewPrompt builds a native prompt without needing a web page.
func webViewPrompt(locale string) mygo.MessageOptions {
	var messages map[string]map[string]string
	if err := json.Unmarshal(nativeMessages, &messages); err != nil {
		panic(err) // embedded, validated by tests
	}
	lang := "en"
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		lang = "zh"
	}
	t := func(key string) string { return messages[lang][key] }
	return mygo.MessageOptions{
		Type: mygo.MessageWarning, Title: t("webview.title"), Message: t("webview.message"),
		Detail: webViewDownloadURL, Buttons: []string{t("webview.download"), t("webview.exit")},
		DefaultButton: 0, CancelButton: 1,
	}
}

// offerWebView opens the download page on confirmation, then exits.
func offerWebView() {
	result, err := mygo.Dialog.Message(webViewPrompt(mygo.App.Locale()))
	if err == nil && result.Button == 0 {
		_ = mygo.Shell.OpenExternal(webViewDownloadURL)
	}
	mygo.App.Exit(0)
}
