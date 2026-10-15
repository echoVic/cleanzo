package main

import (
	"errors"

	"github.com/egoist/mygo"
)

type LanguageSettings struct{}

// SetLanguage keeps MyGo's menus and update window in the page's language.
// An empty locale restores the user's preferred system languages.
func (LanguageSettings) SetLanguage(preference string) (locale string, err error) {
	var tag string
	switch preference {
	case "system":
	case "zh":
		tag = "zh-Hans"
	case "en":
		tag = "en"
	default:
		return "", errors.New("unsupported language preference")
	}
	mygo.RunOnMain(func() {
		mygo.App.SetLocale(tag)
		locale = mygo.App.Locale()
	})
	return
}
