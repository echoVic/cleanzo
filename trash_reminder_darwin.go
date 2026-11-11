//go:build darwin

package main

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// State belongs to the popup's UI thread. Workers publish through Window.Update.
type trashReminderPopup struct {
	size, freed          int64
	phase                string
	text                 panelText
	dark                 func() bool
	start, cancel, close func()
	// resize gives the window the height the phase needs; set with the window.
	resize func()
}

const (
	trashPopupWidth = 520
	// Heights of the popup's content, not of the window: the window takes
	// the height of its content (UseContentSize). The confirmation, whose
	// explanation wraps to two lines, and the progress share this height.
	trashPopupCompactHeight = 170
	// The partial and cancelled results add the line explaining what was
	// kept, which wraps to two lines in English.
	trashPopupResultHeight = 210
)

// trashPopupHeight fits the popup to the phase it shows, so the dialog is
// as tall as what it has to say: the confirmation and the progress share
// the compact height, and only a result that explains what was kept grows.
func trashPopupHeight(phase string) int {
	if phase == "partial" || phase == "cancelled" {
		return trashPopupResultHeight
	}
	return trashPopupCompactHeight
}

func (p *trashReminderPopup) finish(r CleanResult, cancelled bool) {
	p.freed = r.Freed
	p.phase = "done"
	if cancelled {
		p.phase = "cancelled"
	} else if len(r.Failed) > 0 || r.Skipped != "" || r.InUse > 0 || r.Freed < p.size {
		p.phase = "partial"
	}
	if p.resize != nil {
		p.resize()
	}
}

func (p *trashReminderPopup) stop() {
	if p.phase != "cleaning" {
		return
	}
	p.phase = "stopping"
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *trashReminderPopup) view(c *ui.Context) {
	dark := false
	if p.dark != nil {
		dark = p.dark()
	}
	c.SetTheme(menuBarPanelTheme(c.Theme(), dark))
	t := c.Theme()
	text := p.text
	key := "confirm"
	switch p.phase {
	case "cleaning":
		key = "cleaning"
	case "stopping":
		key = "stopping"
	case "done":
		key = "done"
	case "partial":
		key = "partial"
	case "cancelled":
		key = "cancelled"
	}
	ui.Column(c).Fill().Padding(20).Gap(10).Background(t.Background).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "♻").FontSize(32).TextColor(t.Accent)
			ui.Text(c, text("trash.popup."+key, nil)).FontSize(19).Bold().Grow(1).MinWidth(0)
		})
		if p.phase == "" {
			ui.Text(c, text("trash.popup.body", map[string]string{"size": growthSize(p.size)})).FontSize(13).MaxLines(3).TextColor(t.TextMuted)
		} else {
			ui.Progress(c, math.Min(1, float64(p.freed)/float64(max(1, p.size)))).WidthPercent(100)
			ui.Text(c, text("trash.popup.freed", map[string]string{"size": growthSize(p.freed)})).FontSize(13).TextColor(t.TextMuted)
			if p.phase == "partial" || p.phase == "cancelled" {
				ui.Text(c, text("trash.popup.remaining", nil)).FontSize(12).MaxLines(3).TextColor(t.TextMuted)
			}
		}
		ui.Spacer(c).Grow(1)
		ui.Row(c).Justify(ui.End).Gap(10).Children(func() {
			switch p.phase {
			case "":
				ui.Button(c.Key("later"), text("trash.popup.later", nil)).OnClick(p.close)
				ui.PrimaryButton(c.Key("confirm"), text("trash.popup.empty", nil)).OnClick(func() {
					if p.phase != "" {
						return
					}
					p.phase = "cleaning"
					if p.start != nil {
						p.start()
					}
				})
			case "cleaning", "stopping":
				ui.Button(c.Key("cancel"), text("trash.popup.cancel", nil)).Disabled(p.phase == "stopping").OnClick(p.stop)
			default:
				ui.PrimaryButton(c.Key("close"), text("trash.popup.close", nil)).OnClick(p.close)
			}
		})
	})
}

var trashReminderQuitting atomic.Bool

func markTrashReminderQuitting() { trashReminderQuitting.Store(true) }

func showTrashReminder(s trashReminderSnapshot, closed func()) {
	mygo.RunOnMain(func() {
		if trashReminderQuitting.Load() || !trashReminderOn() {
			closed()
			return
		}
		p := &trashReminderPopup{size: s.Size, text: nativeText, dark: menuBarAppDark}
		w := mygo.NewWindow(mygo.WindowOptions{
			Title: nativeText("trash.popup.title", nil),
			Width: trashPopupWidth, Height: trashPopupHeight(""), UseContentSize: true,
			DisableResize: true, DisableMinimize: true, DisableMaximize: true,
			Content: ui.View(p.view),
		})
		// The window takes the height each phase needs: it changes only when
		// a result explains what was kept, and stays put while cleaning.
		p.resize = func() {
			if _, h := w.ContentSize(); h != trashPopupHeight(p.phase) {
				w.SetContentSize(trashPopupWidth, trashPopupHeight(p.phase))
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		var alive atomic.Bool
		alive.Store(true)
		update := func(fn func()) {
			w.Update(func() {
				if alive.Load() {
					fn()
				}
			})
		}
		p.close = w.Close
		p.cancel = cancel
		p.start = func() {
			go func() {
				if !trashCleanupGate.TryLock() {
					update(func() { p.finish(CleanResult{Skipped: "busy"}, false) })
					return
				}
				defer trashCleanupGate.Unlock()
				defer userScan()()
				last := time.Time{}
				r := cleanTrashReminder(ctx, trashReminderEnv(true), s, func(b int64) {
					if time.Since(last) < 50*time.Millisecond {
						return
					}
					last = time.Now()
					update(func() { p.freed = b })
				})
				update(func() { p.finish(r, ctx.Err() != nil) })
			}()
		}
		w.OnClose(func(e *mygo.CloseEvent) {
			if !trashReminderQuitting.Load() && (p.phase == "cleaning" || p.phase == "stopping") {
				e.PreventDefault()
				p.stop()
				w.Invalidate()
			}
		})
		w.OnClosed(func() { alive.Store(false); cancel(); closed() })
		w.Focus()
	})
}
