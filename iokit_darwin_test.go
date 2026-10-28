//go:build darwin

package main

import (
	"os"
	"testing"
)

// onThisMac turns on the strict hardware checks: set CLEANZO_HW=1 on a
// real Mac. CI's virtual machines and the executors' sandboxes lack the
// sensors, the SMC and SMART, so without it the live tests check only what
// they did read.
func onThisMac() bool { return os.Getenv("CLEANZO_HW") != "" }

func TestIOKitLoads(t *testing.T) {
	if !iokitInit() {
		t.Fatal("CoreFoundation, IOKit and libSystem load on every Mac")
	}
	if machTaskSelf == 0 {
		t.Error("mach_task_self_ was not read")
	}
	t.Logf("IOHID %v, CoreGraphics %v, SystemConfiguration %v", hidOK, cgOK, scOK)
	if onThisMac() && !(hidOK && cgOK && scOK) {
		t.Error("on a real Mac every group loads")
	}
}

func TestCFHelpers(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	const text = "MacBook Pro (16-inch, 2023) · 中文"
	s := cfStr(text)
	defer cfRelease(s)
	if got := cfGoString(s); got != text {
		t.Errorf("cfGoString = %q, want %q", got, text)
	}
	n := cfNumber(1 << 40)
	defer cfRelease(n)
	if v, ok := cfInt(n); !ok || v != 1<<40 {
		t.Errorf("cfInt = %d, %v", v, ok)
	}
	if _, ok := cfInt(s); ok {
		t.Error("a CFString is not a number")
	}
	if cfGoString(n) != "" {
		t.Error("a CFNumber is not a string")
	}
	cfRelease(0) // CFRelease(NULL) would trap
	if _, ok := cfInt(0); ok || cfGoString(0) != "" {
		t.Error("NULL reads as nothing")
	}

	dict := cfDictionaryCreateMutable(0, 0, cfKeyCallBacks, cfValueCallBacks)
	defer cfRelease(dict)
	key := cfStr("Watts")
	cfDictionarySetValue(dict, key, n)
	cfRelease(key)
	if v, ok := cfInt(cfDictValue(dict, "Watts")); !ok || v != 1<<40 {
		t.Errorf("cfDictValue(Watts) = %d, %v", v, ok)
	}
	if cfDictValue(dict, "Missing") != 0 || cfDictValue(0, "Watts") != 0 {
		t.Error("a missing key or dictionary reads as 0")
	}
}

func TestCString(t *testing.T) {
	for in, want := range map[string]string{
		"Mac14,10\x00\x00junk": "Mac14,10",
		"no terminator":        "no terminator",
		"":                     "",
	} {
		if got := cString([]byte(in)); got != want {
			t.Errorf("cString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIOEach(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	seen := 0
	ioEach("IOMedia", func(uint32) bool { seen++; return true })
	if seen == 0 {
		t.Skip("no IOMedia here")
	}
	visited := 0
	ioEach("IOMedia", func(uint32) bool { visited++; return false })
	if visited != 1 {
		t.Errorf("ioEach went on after fn returned false: %d visits", visited)
	}
	ioEach("CleanzoNoSuchClass", func(uint32) bool {
		t.Error("visited a class that doesn't exist")
		return true
	})
	if s := ioFirst("CleanzoNoSuchClass"); s != 0 {
		ioObjectRelease(s)
		t.Error("ioFirst found a class that doesn't exist")
	}
}

func TestIOPropReadsTheDeviceTree(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	product := ioRegistryEntryFromPath(0, "IODeviceTree:/product")
	if product == 0 {
		t.Skip("no IODeviceTree:/product here")
	}
	defer ioObjectRelease(product)
	name := ioPropString(product, "product-name")
	t.Logf("product-name %q", name)
	if ref := ioProp(product, "cleanzo-no-such-property"); ref != 0 {
		cfRelease(ref)
		t.Error("a missing property reads as 0")
	}
	if onThisMac() && name == "" {
		t.Error("a real Mac's device tree names the product")
	}
}
