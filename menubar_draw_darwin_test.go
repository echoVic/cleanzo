package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

// decodeReadout decodes an icon and checks its size: w points wide and 18
// tall, at twice that in pixels.
func decodeReadout(t *testing.T, data []byte, w float64) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG (%d bytes): %v", len(data), err)
	}
	wantW, wantH := int(math.Round(w*readoutScale)), int(readoutHeight*readoutScale)
	if b := img.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("size %v, want %d × %d", b, wantW, wantH)
	}
	return img
}

// inkRows is the first and last pixel row with ink, -1 without any, and the
// most alpha a pixel has.
func inkRows(img image.Image) (first, last int, alpha uint32) {
	first, last = -1, -1
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a > 0x2000 { // more than antialiasing's faint edge
				if first < 0 {
					first = y - b.Min.Y
				}
				last = y - b.Min.Y
			}
			alpha = max(alpha, a)
		}
	}
	return first, last, alpha
}

func TestDrawReadoutKeepsTheLinesApart(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	w := readoutWidth(menuBarText{Top: "CPU", Bottom: "100%", Label: true})
	if w < 10 || w > 40 {
		t.Fatalf("width %v points", w)
	}
	draw := func(text menuBarText) image.Image {
		return decodeReadout(t, drawReadouts([]readoutSlot{{text, w}}, true, false), w)
	}
	labelFirst, labelLast, labelAlpha := inkRows(draw(menuBarText{Top: "CPU", Label: true}))
	valueFirst, valueLast, valueAlpha := inkRows(draw(menuBarText{Bottom: "51%", Label: true}))
	zhFirst, zhLast, _ := inkRows(draw(menuBarText{Top: "内存", Label: true}))
	t.Logf("label rows %d–%d, 内存 %d–%d, value %d–%d", labelFirst, labelLast, zhFirst, zhLast, valueFirst, valueLast)
	if labelFirst < 0 || valueFirst < 0 || zhFirst < 0 {
		t.Fatal("a line drew nothing")
	}
	if labelLast+1 >= valueFirst || zhLast+1 >= valueFirst { // at least one clear row
		t.Errorf("the labels reach pixel rows %d and %d; the value starts at %d", labelLast, zhLast, valueFirst)
	}
	if labelFirst > 12 || valueLast < 24 {
		t.Errorf("label from row %d, value to row %d: want the label at the top, the value at the bottom", labelFirst, valueLast)
	}
	if labelAlpha >= valueAlpha {
		t.Errorf("the label (alpha %#x) is not lighter than the value (%#x)", labelAlpha, valueAlpha)
	}
}

func TestDrawReadoutTemplateIsBlack(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	text := menuBarText{Top: "MEM", Bottom: "86%", Label: true}
	w := readoutWidth(text)
	img := decodeReadout(t, drawReadouts([]readoutSlot{{text, w}}, true, false), w)
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl, a := img.At(x, y).RGBA(); a > 0 && (r != 0 || g != 0 || bl != 0) {
				t.Fatalf("pixel %d,%d is not black: %d %d %d %d", x, y, r, g, bl, a)
			}
		}
	}
}

func TestDrawReadoutColorsTheValueOnADarkBar(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	text := menuBarText{Top: "MEM", Bottom: "86%", Label: true, Tone: "warn"}
	w := readoutWidth(text)
	img := decodeReadout(t, drawReadouts([]readoutSlot{{text, w}}, false, true), w)
	var orange, light bool
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A > 200 && c.R > 220 && c.G > 150 && c.G < 230 && c.B < 140 {
				orange = true // #FFC35A
			}
			if c.A > 60 && c.R > 200 && c.G > 200 && c.B > 200 {
				light = true // the label, white at 55%
			}
		}
	}
	if !orange || !light {
		t.Errorf("orange value %v, light label %v: want both on a dark bar", orange, light)
	}
}

func TestReadoutWidthHoldsTheWidest(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	for _, tc := range []struct{ widest, actual menuBarText }{
		{menuBarText{Top: "CPU", Bottom: "100%", Label: true}, menuBarText{Top: "CPU", Bottom: "5%", Label: true}},
		{menuBarText{Top: "↓000M", Bottom: "↑000M"}, menuBarText{Top: "↓123M", Bottom: "↑123G"}},
		{menuBarText{Top: "磁盘", Bottom: "000.0 GB 可用", Label: true}, menuBarText{Top: "磁盘", Bottom: "26.7 GB 可用", Label: true}},
	} {
		if w, a := readoutWidth(tc.widest), readoutWidth(tc.actual); w <= 0 || a > w {
			t.Errorf("%+v: width %v, but %+v needs %v", tc.widest, w, tc.actual, a)
		}
	}
}

// inkIn tells whether any pixel in columns [x0, x1) has more than
// antialiasing's faint edge.
func inkIn(img image.Image, x0, x1 int) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X + x0; x < b.Min.X+x1 && x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x2000 {
				return true
			}
		}
	}
	return false
}

func TestDrawReadoutsPutsEachReadingInItsSlot(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	cpu := menuBarText{Top: "CPU", Bottom: "100%", Label: true}
	mem := menuBarText{Top: "内存", Bottom: "86%", Label: true}
	w1, w2 := readoutWidth(cpu), readoutWidth(mem)
	total := readoutsWidth([]float64{w1, w2})
	img := decodeReadout(t, drawReadouts([]readoutSlot{{cpu, w1}, {mem, w2}}, true, false), total)
	px := func(p float64) int { return int(math.Round(p * readoutScale)) }
	if !inkIn(img, 0, px(w1)) || !inkIn(img, px(w1+readoutGap), px(total)) {
		t.Error("a reading drew nothing in its slot")
	}
	if inkIn(img, px(w1), px(w1+readoutGap)) {
		t.Errorf("ink in the gap, pixel columns %d–%d", px(w1), px(w1+readoutGap))
	}
}

func TestDrawReadoutsColorsOnlyTheTonedValue(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	cpu := menuBarText{Top: "CPU", Bottom: "43%", Label: true}
	mem := menuBarText{Top: "MEM", Bottom: "86%", Label: true, Tone: "warn"}
	w1, w2 := readoutWidth(cpu), readoutWidth(mem)
	img := decodeReadout(t, drawReadouts([]readoutSlot{{cpu, w1}, {mem, w2}}, false, true), readoutsWidth([]float64{w1, w2}))
	split := int(math.Round((w1 + readoutGap/2) * readoutScale))
	find := func(x0, x1 int, match func(color.NRGBA) bool) bool {
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X + x0; x < b.Min.X+x1 && x < b.Max.X; x++ {
				if match(color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)) {
					return true
				}
			}
		}
		return false
	}
	orange := func(c color.NRGBA) bool { return c.A > 200 && c.R > 220 && c.G > 150 && c.G < 230 && c.B < 140 } // #FFC35A
	white := func(c color.NRGBA) bool { return c.A > 200 && c.R > 220 && c.G > 220 && c.B > 220 }
	if find(0, split, orange) || !find(0, split, white) {
		t.Error("CPU's value: want the dark bar's light ink, not orange")
	}
	if !find(split, img.Bounds().Dx(), orange) {
		t.Error("memory's value is not orange")
	}
}
