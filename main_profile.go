//go:build profile

package main

// A build for measuring Atlas rather than for running it: `make build-profile`.
//
// Set ATLAS_CPUPROFILE to a path and the whole run is recorded as a pprof CPU
// profile; ATLAS_MEMPROFILE adds a heap profile taken when the process is told to
// stop. The run ends on SIGTERM or SIGINT, which is what flushes both — a profile
// that is never stopped is empty.
//
// Time spent inside GTK shows up as runtime.cgocall under whichever Go function
// made the call, which is usually what is wanted: it says which of Atlas's own
// decisions the drawing time belongs to.
//
// It is a build tag rather than an environment variable in the normal binary so
// that the shipped program carries none of it.

import (
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
)

func init() {
	cpuPath := os.Getenv("ATLAS_CPUPROFILE")
	memPath := os.Getenv("ATLAS_MEMPROFILE")
	if cpuPath == "" && memPath == "" {
		return
	}
	var cpu *os.File
	if cpuPath != "" {
		f, err := os.Create(cpuPath)
		if err == nil && pprof.StartCPUProfile(f) == nil {
			cpu = f
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		if cpu != nil {
			pprof.StopCPUProfile()
			cpu.Close()
		}
		if memPath != "" {
			if f, err := os.Create(memPath); err == nil {
				runtime.GC() // a heap profile reports live objects as of the last GC
				pprof.WriteHeapProfile(f)
				f.Close()
			}
		}
		os.Exit(0)
	}()
}
