package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// The page's own menu offers Reload, Back and Forward, which would navigate
// the app's single page away, so the app removes them and keeps the editing
// items. A link opens in the user's browser instead, named in the app's
// language.
func TestCleanzoContextMenu(t *testing.T) {
	for _, tt := range []struct {
		locale, openLink string
	}{
		{"zh-Hans", "在浏览器中打开链接"},
		{"en", "Open Link in Browser"},
	} {
		t.Run(tt.locale, func(t *testing.T) {
			menu := mygo.NewMenu([]*mygo.MenuItem{
				{ID: mygo.ContextMenuReload, Label: "Reload"},
				{ID: mygo.ContextMenuBack, Label: "Back"},
				{ID: mygo.ContextMenuForward, Label: "Forward"},
				{ID: mygo.ContextMenuCopy, Label: "Copy"},
				{ID: mygo.ContextMenuPaste, Label: "Paste"},
			})
			e := &mygo.ContextMenuEvent{Menu: menu, LinkURL: "https://example.com/docs"}
			cleanzoContextMenu(e, tt.locale)
			for _, id := range []string{mygo.ContextMenuReload, mygo.ContextMenuBack, mygo.ContextMenuForward} {
				if menu.ItemByID(id) != nil {
					t.Errorf("the menu kept the page's own item %q", id)
				}
			}
			for _, id := range []string{mygo.ContextMenuCopy, mygo.ContextMenuPaste} {
				if menu.ItemByID(id) == nil {
					t.Errorf("the menu lost the editing item %q", id)
				}
			}
			var opened *mygo.MenuItem
			for _, item := range menu.Items() {
				if item.Label == tt.openLink {
					opened = item
				}
			}
			if opened == nil || opened.Click == nil {
				t.Fatalf("the menu has no %q item that opens the link", tt.openLink)
			}
		})
	}
}

func TestCleanzoContextMenuLeavesOtherTargetsAlone(t *testing.T) {
	menu := mygo.NewMenu([]*mygo.MenuItem{{ID: mygo.ContextMenuCopy, Label: "Copy"}})
	e := &mygo.ContextMenuEvent{Menu: menu}
	cleanzoContextMenu(e, "en")
	if len(menu.Items()) != 1 || menu.ItemByID(mygo.ContextMenuCopy) == nil {
		t.Fatalf("a menu over no link changed: %d items", len(menu.Items()))
	}
}
