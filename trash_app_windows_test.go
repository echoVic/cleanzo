package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/egoist/mygo"
)

// init gives native Shell test children the main-thread COM and dispatch loop required by mygo.
func init() {
	if os.Getenv("CLEANZO_SHELL_TEST") == "" {
		return
	}
	runPlatformTests = func(m *testing.M) int {
		result := make(chan int, 1)
		mygo.App.WhenReady(func() {
			go func() {
				result <- m.Run()
				mygo.App.Quit()
			}()
		})
		if err := mygo.App.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "native shell test app:", err)
			return 1
		}
		return <-result
	}
}
