//go:build darwin

package main

import "testing"

// TestIslandNotchOfAScreen: the notch is where the strips to its sides leave
// it, at the top of its screen, wherever that screen is arranged; a screen
// without a notch, or without room beside one, has none.
func TestIslandNotchOfAScreen(t *testing.T) {
	cases := []struct {
		name             string
		primaryHeight    float64
		frame            nsRect
		top, left, right float64
		want             islandNotch
		ok               bool
	}{
		{"the built-in display alone", 1117, nsRect{0, 0, 1728, 1117}, 32, 771, 772, islandNotch{X: 771, Y: 0, Width: 185, Height: 32}, true},
		{"to the right of an external primary, tops in line", 1080, nsRect{1920, -37, 1728, 1117}, 32, 771, 772, islandNotch{X: 2691, Y: 0, Width: 185, Height: 32}, true},
		{"under an external primary", 1440, nsRect{416, -1117, 1728, 1117}, 32, 771, 772, islandNotch{X: 1187, Y: 1440, Width: 185, Height: 32}, true},
		{"a smaller built-in display", 982, nsRect{0, 0, 1512, 982}, 38, 656.5, 656.5, islandNotch{X: 657, Y: 0, Width: 199, Height: 38}, true},
		{"a display without a notch", 1080, nsRect{0, 0, 1920, 1080}, 0, 0, 0, islandNotch{}, false},
		{"a notch with no strip beside it", 1117, nsRect{0, 0, 1728, 1117}, 32, 0, 0, islandNotch{}, false},
		{"strips that leave no notch", 1117, nsRect{0, 0, 1728, 1117}, 32, 864, 864, islandNotch{}, false},
	}
	for _, c := range cases {
		got, ok := islandNotchOf(c.primaryHeight, c.frame, c.top, c.left, c.right)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestIslandNotchOnThisMac reads the notch of this Mac's display where it
// has one, and checks AppKit's answers make a notch of a believable size.
func TestIslandNotchOnThisMac(t *testing.T) {
	viewMainHere(t)
	n, ok := readIslandNotch()
	if !ok {
		t.Skip("no display with a notch")
	}
	if n.Width < 80 || n.Width > 400 || n.Height < 16 || n.Height > 80 {
		t.Fatalf("notch %+v", n)
	}
	t.Logf("notch %+v", n)
}
