//go:build linux && cgo

// Package sysmem tunes the C allocator and hands idle memory back to the
// kernel. Atlas is a long-lived desktop app built on GTK, so most of its
// resident set is glibc-managed memory that GTK, Pango and cairo have freed but
// the allocator is still holding: without an occasional trim the process keeps
// every page it ever peaked at.
package sysmem

/*
#include <stdlib.h>

// mallopt and malloc_trim are glibc extensions. Everything here is a no-op on a
// libc that does not have them, so the package still builds.
#if defined(__GLIBC__)
#include <malloc.h>

static void atlas_tune(void) {
  mallopt(M_ARENA_MAX, 2);
  mallopt(M_TRIM_THRESHOLD, 128 * 1024);
  mallopt(M_TOP_PAD, 0);
  mallopt(M_MMAP_THRESHOLD, 128 * 1024);
}

static void atlas_trim(void) { malloc_trim(0); }

#else
static void atlas_tune(void) {}
static void atlas_trim(void) {}
#endif
*/
import "C"

import "runtime/debug"

// Tune configures glibc's allocator for a small, long-running GUI process:
//
//   - M_ARENA_MAX caps the number of per-thread arenas. The default scales with
//     the core count, and a 32-thread process can end up with dozens of 64 MiB
//     arenas, each fragmenting independently. Two is plenty — only the GTK main
//     thread allocates heavily.
//   - M_TRIM_THRESHOLD / M_TOP_PAD stop the heap from growing in large steps and
//     let free memory at the top of the heap go back to the kernel promptly.
//   - M_MMAP_THRESHOLD keeps large blocks (pixel buffers, icon caches) in their
//     own mappings, which are returned to the kernel the moment they are freed
//     instead of leaving a hole in the heap.
//
// Call once, as early as possible.
func Tune() { C.atlas_tune() }

// Trim releases free C-heap pages back to the kernel. It is cheap (a walk of
// the arenas' free chunks) and safe to call periodically, from any goroutine.
func Trim() { C.atlas_trim() }

// Release is the full sweep used when the window is hidden: return unused Go
// spans to the OS, then trim the C heap. A backgrounded Atlas should cost the
// system almost nothing.
func Release() {
	debug.FreeOSMemory()
	C.atlas_trim()
}
