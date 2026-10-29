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
		return decodeReadout(t, drawReadout(text, w, readoutInk{template: true}), w)
	}
	labelFirst, labelLast, labelAlpha := inkRows(draw(menuBarText{Top: "CPU", Label: true}))
	valueFirst, valueLast, valueAlpha := inkRows(draw(menuBarText{Bottom: "51%", Label: true}))
	zhFirst, zhLast, _ := inkRows(draw(menuBarText{Top: "内存", Label: true}))
	t.Logf("label rows %d–%d, 内存 %d–%d, value %d–%d", labelFirst, labelLast, zhFirst, zhLast, valueFirst, valueLast)
	if labelFirst < 0 || valueFirst < 0 || zhFirst < 0 {
		t.Fatal("a line drew nothing")
	}
	if labelLast >= valueFirst || zhLast >= valueFirst {
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
	img := decodeReadout(t, drawReadout(text, w, readoutInk{template: true}), w)
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
	img := decodeReadout(t, drawReadout(text, w, readoutInk{dark: true, tone: "warn"}), w)
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
		{menuBarText{Top: "↓000K", Bottom: "↑000K"}, menuBarText{Top: "↓1.2M", Bottom: "↑55K"}},
		{menuBarText{Top: "磁盘", Bottom: "000.0 GB 可用", Label: true}, menuBarText{Top: "磁盘", Bottom: "26.7 GB 可用", Label: true}},
	} {
		if w, a := readoutWidth(tc.widest), readoutWidth(tc.actual); w <= 0 || a > w {
			t.Errorf("%+v: width %v, but %+v needs %v", tc.widest, w, tc.actual, a)
		}
	}
}
