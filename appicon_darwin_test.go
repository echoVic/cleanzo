package main

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
)

const testIconApp = "/System/Applications/Calculator.app"

func TestNativeAppIconDrawsTheIcon(t *testing.T) {
	if _, err := os.Stat(testIconApp); err != nil {
		t.Skip("no Calculator.app on this Mac")
	}
	data := nativeAppIcon(testIconApp, appIconPx)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG (%d bytes): %v", len(data), err)
	}
	if b := img.Bounds(); b.Dx() != appIconPx || b.Dy() != appIconPx {
		t.Fatalf("size %v, want %d × %d", b, appIconPx, appIconPx)
	}
	if _, _, _, a := img.At(appIconPx/2, appIconPx/2).RGBA(); a != 0xffff {
		t.Fatalf("the middle of the icon has alpha %#x, want fully opaque (0xffff)", a)
	}
}

func TestAppIconOfAnInstalledApp(t *testing.T) {
	if _, err := os.Stat(testIconApp); err != nil {
		t.Skip("no Calculator.app on this Mac")
	}
	fakeAppIcons(t, nil) // only for the empty cache and its cleanup
	appIconRender = nativeAppIcon
	url, err := (Cleaner{}).AppIcon(testIconApp)
	if err != nil || !strings.HasPrefix(url, "data:image/png;base64,") || len(url) < 1000 {
		t.Fatalf("AppIcon = %.60q…, %v", url, err)
	}
}
