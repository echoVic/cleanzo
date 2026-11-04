package main

import (
	"os"
	"path/filepath"
)

// refreshShellIconOnce records a successfully refreshed icon fingerprint.
func refreshShellIconOnce(marker, fingerprint string, refresh func() error) error {
	if fingerprint == "" {
		return nil // tests and unpackaged development builds have no version
	}
	previous, err := os.ReadFile(marker)
	if err == nil && string(previous) == fingerprint {
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(fingerprint), 0o600)
}
