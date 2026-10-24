package main

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

const (
	qosClassBackground = 0x09 // QOS_CLASS_BACKGROUND, <sys/qos.h>
	iopolTypeDisk      = 0    // IOPOL_TYPE_DISK, <sys/resource.h>
	iopolScopeThread   = 1    // IOPOL_SCOPE_THREAD
	iopolThrottle      = 3    // IOPOL_THROTTLE
)

var (
	qosOnce     sync.Once
	setQoS      func(class uint32, relpri int32) int32
	setIOPolicy func(iotype, scope, policy int32) int32
)

// backgroundThread pins the calling goroutine to its OS thread. It lowers
// that thread to background QoS with throttled disk I/O, so measuring yields
// to everything the user does (spec §四).
//
// The goroutine must return without unlocking. Go then retires the thread
// and its policy instead of reusing it.
func backgroundThread() {
	runtime.LockOSThread()
	qosOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&setQoS, lib, "pthread_set_qos_class_self_np")
		purego.RegisterLibFunc(&setIOPolicy, lib, "setiopolicy_np")
	})
	if setQoS != nil {
		setQoS(qosClassBackground, 0)
	}
	if setIOPolicy != nil {
		setIOPolicy(iopolTypeDisk, iopolScopeThread, iopolThrottle)
	}
}
