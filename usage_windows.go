package main

// openFiles fails closed until Windows usage probes are implemented.
func openFiles() ([]string, error) { return nil, errUnsupported }

// runningApps fails closed until Windows process snapshots are implemented.
func runningApps() (map[string]bool, []string, error) { return nil, nil, errUnsupported }
