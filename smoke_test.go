package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// TestSmokeTour checks complete artifacts, theme order and the failure report.
func TestSmokeTour(t *testing.T) {
	for _, failure := range []string{"", "console", "capture", "navigate"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			var visits []string
			var themes []mygo.ThemeSource
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
			driver := smokeDriver{
				eval: func(_ context.Context, code string) (any, error) {
					if strings.Contains(code, ".errors") {
						if failure == "console" {
							return []any{"console.error: broken"}, nil
						}
						return []any{}, nil
					}
					if strings.Contains(code, ".visit(") {
						visits = append(visits, code)
						if failure == "navigate" {
							return nil, errors.New("view missing")
						}
					}
					return true, nil
				},
				capture: func() ([]byte, error) {
					if failure == "capture" {
						return nil, errors.New("capture failed")
					}
					return png, nil
				},
				theme: func(source mygo.ThemeSource) { themes = append(themes, source) },
			}
			err := smokeTour(context.Background(), dir, driver)
			if (err != nil) != (failure != "") {
				t.Fatalf("result = %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "errors.json"))
			if err != nil {
				t.Fatal(err)
			}
			var reported []string
			if err := json.Unmarshal(raw, &reported); err != nil || (len(reported) > 0) != (failure != "") {
				t.Fatalf("report = %s, error = %v", raw, err)
			}
			if failure != "" {
				return
			}
			if len(visits) != 18 || len(themes) != 2 || themes[0] != mygo.ThemeLight || themes[1] != mygo.ThemeDark {
				t.Fatalf("visits = %v, themes = %v", visits, themes)
			}
			for _, theme := range []string{"light", "dark"} {
				for _, view := range smokeViews {
					if info, err := os.Stat(filepath.Join(dir, theme+"-"+view+".png")); err != nil || info.Size() == 0 {
						t.Fatalf("%s/%s screenshot missing: %v", theme, view, err)
					}
				}
			}
		})
	}
}
