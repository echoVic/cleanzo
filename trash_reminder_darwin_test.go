//go:build darwin

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTrashReminderPopupFlow(t *testing.T) {
	started, cancelled, closed, resized := 0, 0, 0, 0
	p := &trashReminderPopup{size: 2 * gb, text: panelViewText, start: func() { started++ }, cancel: func() { cancelled++ }, close: func() { closed++ }, resize: func() { resized++ }}
	tt := ui.NewTester(p.view, trashPopupWidth, trashPopupHeight(p.phase))
	if !tt.HasText("清空废纸篓？") || !tt.HasText("清空（永久删除）") {
		t.Fatal("missing confirmation")
	}
	if started != 0 {
		t.Fatal("render started deletion")
	}
	if err := tt.Click("清空（永久删除）"); err != nil {
		t.Fatal(err)
	}
	if started != 1 || p.phase != "cleaning" {
		t.Fatal("did not start")
	}
	if resized != 1 {
		t.Fatal("did not resize for the progress")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || p.phase != "stopping" {
		t.Fatal("did not cancel")
	}
	if resized != 2 {
		t.Fatal("did not resize for the cancel")
	}
	p.finish(CleanResult{Freed: gb}, true)
	tt.Frame()
	if !tt.HasText("已停止清理") {
		t.Fatal("cancellation reported as success")
	}
	if resized != 3 {
		t.Fatal("did not resize for the result")
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatal("did not close")
	}
}

func TestTrashReminderPopupPreviews(t *testing.T) {
	dir := os.Getenv("CLEANZO_TRASH_PREVIEWS")
	if dir == "" {
		t.Skip("set CLEANZO_TRASH_PREVIEWS to save popup renders")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"zh", "en"} {
		for _, dark := range []bool{false, true} {
			for _, phase := range []string{"", "cleaning", "cancelled", "partial", "done"} {
				p := &trashReminderPopup{size: 2 * gb, freed: gb, phase: phase, text: func(key string, vars map[string]string) string { return nativeTextFor(lang, key, vars) }, dark: func() bool { return dark }}
				name := phase
				if name == "" {
					name = "confirm"
				}
				mode := "light"
				if dark {
					mode = "dark"
				}
				f, err := os.Create(filepath.Join(dir, lang+"-"+mode+"-"+name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, ui.Render(p.view, trashPopupWidth, trashPopupHeight(phase), 2))
				closeErr := f.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			}
		}
	}
}

func TestTrashReminderPopupPartialAndLater(t *testing.T) {
	closed := 0
	p := &trashReminderPopup{size: gb, text: panelViewText, close: func() { closed++ }}
	tt := ui.NewTester(p.view, trashPopupWidth, trashPopupHeight(p.phase))
	if err := tt.Click("暂不清理"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatal("later did not dismiss")
	}
	p.finish(CleanResult{Freed: 100, Failed: []string{"blocked"}}, false)
	tt.Frame()
	if !tt.HasText("部分内容未清理") {
		t.Fatal("partial result hidden")
	}
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
}

// TestTrashReminderPopupFit checks that every phase's longest real content
// still leaves the button row inside the window at the height
// trashPopupHeight gives it, so a wrapped line can't push the button out.
func TestTrashReminderPopupFit(t *testing.T) {
	buttonKeys := map[string]string{
		"":          "trash.popup.empty",
		"cleaning":  "trash.popup.cancel",
		"done":      "trash.popup.close",
		"partial":   "trash.popup.close",
		"cancelled": "trash.popup.close",
	}
	for _, lang := range []string{"zh", "en"} {
		text := func(key string, vars map[string]string) string { return nativeTextFor(lang, key, vars) }
		for _, phase := range []string{"", "cleaning", "done", "partial", "cancelled"} {
			p := &trashReminderPopup{size: 2 * gb, freed: gb, phase: phase, text: text, dark: func() bool { return false }}
			h := trashPopupHeight(phase)
			tt := ui.NewTester(p.view, trashPopupWidth, h)
			button, ok := tt.Find(text(buttonKeys[phase], nil))
			if !ok {
				t.Fatalf("%s %q: button missing", lang, phase)
			}
			// The row keeps its 20-point bottom padding; a line wrapped by a
			// wider real rendering would push it past that.
			if bottom := button.Y + button.H; bottom > float32(h-20) {
				t.Errorf("%s %q: button reaches %.1f, past the bottom padding at height %d", lang, phase, bottom, h)
			}
		}
	}
}
