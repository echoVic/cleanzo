//go:build race

package main

// raceDetector reports whether tests run under the race detector, which
// makes timing checks meaningless.
const raceDetector = true
