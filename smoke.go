package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

// smokeScript captures errors from document start and clicks the real UI.
//
//go:embed tests/smoke/preload.js
var smokeScript string

// smokeViews is the complete sidebar tour, in capture order.
var smokeViews = []string{"status", "clean", "aitools", "skills", "fullscan", "large", "photos", "apps", "settings"}

// smokeDriver keeps the tour testable without a native window.
type smokeDriver struct {
	eval    func(context.Context, string) (any, error)
	capture func() ([]byte, error)
	theme   func(mygo.ThemeSource)
}

// startSmoke waits for the loaded page before starting the bounded tour.
func startSmoke(w *mygo.Window, dir string) {
	loaded := make(chan struct{}, 1)
	off := w.Page().OnDidFinishLoad(func() {
		select {
		case loaded <- struct{}{}:
		default:
		}
	})
	go func() {
		defer off()
		ctx, cancel := context.WithTimeout(context.Background(), 270*time.Second)
		defer cancel()
		select {
		case <-loaded:
		case <-ctx.Done():
		}
		err := smokeTour(ctx, dir, smokeDriver{w.Page().EvalContext, w.CapturePage, mygo.Theme.SetSource})
		if err != nil {
			log.Print(err)
			mygo.App.Exit(1)
		}
		mygo.App.Exit(0)
	}()
}

// smokeTour saves both appearances and always attempts to write its error report.
func smokeTour(ctx context.Context, dir string, d smokeDriver) (err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	defer func() {
		// 即使巡检超时，也留出短时间读取页面错误。
		reportCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		reported := []string{}
		value, readErr := d.eval(reportCtx, "window.__cleanzoSmoke.errors")
		if readErr != nil {
			reported = append(reported, "read errors: "+readErr.Error())
		} else if items, ok := value.([]any); ok {
			for _, item := range items {
				reported = append(reported, fmt.Sprint(item))
			}
		} else {
			reported = append(reported, "smoke: missing error collector")
		}
		if err != nil {
			reported = append(reported, "smoke: "+err.Error())
		}
		raw, _ := json.MarshalIndent(reported, "", "  ")
		if writeErr := os.WriteFile(filepath.Join(dir, "errors.json"), raw, 0o600); writeErr != nil {
			err = fmt.Errorf("write smoke errors: %w (tour: %v)", writeErr, err)
		} else if err == nil && len(reported) > 0 {
			err = fmt.Errorf("smoke: %d page errors", len(reported))
		}
	}()
	if _, err = d.eval(ctx, "window.__cleanzoSmoke.waitReady()"); err != nil {
		return err
	}
	for _, theme := range []mygo.ThemeSource{mygo.ThemeLight, mygo.ThemeDark} {
		d.theme(theme)
		if _, err = d.eval(ctx, fmt.Sprintf("window.__cleanzoSmoke.waitTheme(%t)", theme == mygo.ThemeDark)); err != nil {
			return err
		}
		for _, view := range smokeViews {
			if _, err = d.eval(ctx, fmt.Sprintf("window.__cleanzoSmoke.visit(%q)", view)); err != nil {
				return fmt.Errorf("%s/%s: %w", theme, view, err)
			}
			data, captureErr := d.capture()
			if captureErr != nil {
				return fmt.Errorf("%s/%s capture: %w", theme, view, captureErr)
			}
			if _, err = png.DecodeConfig(bytes.NewReader(data)); err != nil {
				return fmt.Errorf("%s/%s PNG: %w", theme, view, err)
			}
			if err = os.WriteFile(filepath.Join(dir, string(theme)+"-"+view+".png"), data, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}
