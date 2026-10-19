package main

import (
	"os"
	"regexp"
	"testing"
)

// The update signing key stays out of the repository: release builds get it
// from MYGO_UPDATER_PRIVATE_KEY (docs/RELEASING.md).
func TestUpdateConfiguration(t *testing.T) {
	data, err := os.ReadFile("mygo.config.ts")
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if !regexp.MustCompile(`(?m)^\s*github:\s*"echoVic/cleanzo"`).MatchString(config) {
		t.Error(`updates.github must be "echoVic/cleanzo"`)
	}
	if !regexp.MustCompile(`(?m)^\s*publicKey:\s*"[^"]+"`).MatchString(config) {
		t.Error("updates.publicKey must not be empty")
	}
	if regexp.MustCompile(`(?m)^\s*privateKey\s*:`).MatchString(config) {
		t.Error("updates.privateKey must not be configured: release builds get the key from MYGO_UPDATER_PRIVATE_KEY")
	}
}
