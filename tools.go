package main

// toolCommand is filled in by the tool cleanups.
type toolCommand struct{}

// Tool cleanup placeholders stay disabled until the tool cleanups are implemented.
var brewCleanup, pnpmPrune, goModCleanup, simctlCleanup toolCommand
