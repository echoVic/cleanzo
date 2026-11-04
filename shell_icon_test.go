package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestShellIconRefreshAfterUpgrade(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "Cleanzo", "shell-icon")
	calls := 0
	refresh := func() error { calls++; return nil }
	for _, fingerprint := range []string{"green-v1", "green-v1", "green-v2", "green-v2"} {
		if err := refreshShellIconOnce(marker, fingerprint, refresh); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("refresh calls = %d; want once initially and once after icon change", calls)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "green-v2" {
		t.Fatalf("marker = %q, %v", data, err)
	}
}

func TestShellIconRefreshRetriesFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "shell-icon")
	failure := errors.New("notification failed")
	if err := refreshShellIconOnce(marker, "green", func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("error = %v; want notification error", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed notification must not mark success: %v", err)
	}
	calls := 0
	if err := refreshShellIconOnce(marker, "green", func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("retry calls = %d, error = %v", calls, err)
	}
}

func TestShellIconRefreshSkipsUnversionedBuild(t *testing.T) {
	if err := refreshShellIconOnce(filepath.Join(t.TempDir(), "marker"), "", func() error {
		t.Fatal("unversioned build must not refresh Shell")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
