package main

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestSmartLogInfo(t *testing.T) {
	page := make([]byte, 512)
	page[0] = 0x05                                // critical warning: spare and reliability bits
	binary.LittleEndian.PutUint16(page[1:3], 306) // 33 °C in kelvin
	page[3] = 100                                 // available spare
	page[5] = 26                                  // percentage used
	binary.LittleEndian.PutUint64(page[48:56], 1219727)
	var ssd SSDInfo
	if !smartLogInfo(page, &ssd) {
		t.Fatal("a full log page reads")
	}
	want := SSDInfo{SMART: true, Warning: 5, TempC: 33, Spare: 100, Used: 26, Written: 1219727 * 512000}
	if ssd != want {
		t.Errorf("got %+v, want %+v", ssd, want)
	}

	var short SSDInfo
	if smartLogInfo(page[:63], &short) || short.SMART {
		t.Error("a short page must not count as read")
	}

	binary.LittleEndian.PutUint16(page[1:3], 0)
	var cold SSDInfo
	smartLogInfo(page, &cold)
	if cold.TempC != 0 {
		t.Errorf("an unreported temperature stays 0, got %d", cold.TempC)
	}
}

func TestDisplaySignature(t *testing.T) {
	ids := []uint32{3, 1}
	if got := displaySignature(ids); got != "1,3" {
		t.Errorf("displaySignature = %q, want 1,3", got)
	}
	if !slices.Equal(ids, []uint32{3, 1}) {
		t.Error("displaySignature must not reorder its argument")
	}
	if displaySignature([]uint32{1, 3}) != displaySignature([]uint32{3, 1}) {
		t.Error("the order of the displays doesn't matter")
	}
	if displaySignature(nil) != "" {
		t.Error("no displays, empty signature")
	}
}
