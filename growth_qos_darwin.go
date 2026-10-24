package main

import (
	"log"
	"runtime"
	"syscall"
)

// Darwin's setpriority(2) targets, from <sys/resource.h>. With PRIO_DARWIN_THREAD
// and who 0 the call names the calling thread, and PRIO_DARWIN_BG puts that
// thread in the background state.
const (
	prioDarwinThread = 3      // PRIO_DARWIN_THREAD
	prioDarwinBG     = 0x1000 // PRIO_DARWIN_BG
)

// backgroundThread pins the calling goroutine to its OS thread and puts that
// thread in Darwin's background state with one setpriority(2) call. The
// kernel then runs the thread at the lowest CPU priority and throttles its
// disk I/O, so measuring yields to everything the user does (spec §四). If the
// kernel refuses, the thread keeps its normal priority and measuring goes on.
//
// The goroutine must return without unlocking. Go then retires the thread
// and its state instead of reusing it.
func backgroundThread() {
	runtime.LockOSThread()
	if err := syscall.Setpriority(prioDarwinThread, 0, prioDarwinBG); err != nil {
		log.Printf("growth: measuring at normal priority: %v", err)
	}
}
