package winapi

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// parseProcesses walks a kernel-filled buffer with unsafe pointer arithmetic,
// which makes it the most dangerous function in this package: a mistake in the
// traversal reads whatever happens to be next in memory, and there is no crash to
// tell you. So it is driven with a buffer built here, where what it should find is
// known.

// buildProcessBuffer lays out entries the way the kernel does: each one carries the
// byte offset to the next, and the last carries zero.
func buildProcessBuffer(t *testing.T, entries []windows.SYSTEM_PROCESS_INFORMATION, names []string) []byte {
	t.Helper()
	const size = int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))

	// The name buffers live after the entries, so the pointers into them stay
	// valid for as long as the whole slice does.
	nameStore := make([][]uint16, len(names))
	for i, n := range names {
		if n == "" {
			continue
		}
		nameStore[i] = windows.StringToUTF16(n)
	}

	buf := make([]byte, size*len(entries))
	for i := range entries {
		e := entries[i]
		if nameStore[i] != nil {
			e.ImageName = windows.NTUnicodeString{
				// Length is in bytes and excludes the terminator, which is the
				// detail worth pinning: dividing it by two gives the character
				// count, and getting that wrong truncates or overruns every name.
				Length:        uint16((len(nameStore[i]) - 1) * 2),
				MaximumLength: uint16(len(nameStore[i]) * 2),
				Buffer:        &nameStore[i][0],
			}
		}
		if i == len(entries)-1 {
			e.NextEntryOffset = 0
		} else {
			e.NextEntryOffset = uint32(size)
		}
		*(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[i*size])) = e
	}
	// Keep the name storage alive until the test is done with the buffer.
	t.Cleanup(func() { _ = nameStore })
	return buf
}

func TestParseProcessesReadsEveryEntry(t *testing.T) {
	entries := []windows.SYSTEM_PROCESS_INFORMATION{
		{
			UniqueProcessID:              0,
			InheritedFromUniqueProcessID: 0,
			NumberOfThreads:              4,
		},
		{
			UniqueProcessID:              1234,
			InheritedFromUniqueProcessID: 4,
			UserTime:                     2 * 1e7, // two seconds
			KernelTime:                   1 * 1e7, // one second
			WorkingSetSize:               64 << 20,
			PrivatePageCount:             32 << 20,
			ReadTransferCount:            1000,
			WriteTransferCount:           2000,
			NumberOfThreads:              12,
			SessionID:                    1,
		},
		{
			UniqueProcessID: 5678,
			NumberOfThreads: 1,
		},
	}
	buf := buildProcessBuffer(t, entries, []string{"", "firefox.exe", "notepad.exe"})

	got := parseProcesses(buf)
	if len(got) != 3 {
		t.Fatalf("got %d processes, want 3", len(got))
	}

	// The idle process has no image name and is given one, so it is not a blank row.
	if got[0].PID != 0 || got[0].Name != "System Idle Process" {
		t.Errorf("first entry = %+v, want pid 0 named as the idle process", got[0])
	}

	p := got[1]
	if p.PID != 1234 || p.ParentPID != 4 {
		t.Errorf("pid/ppid = %d/%d, want 1234/4", p.PID, p.ParentPID)
	}
	if p.Name != "firefox.exe" {
		t.Errorf("name = %q, want %q — the counted length is in bytes, not characters", p.Name, "firefox.exe")
	}
	// User and kernel time are summed: three seconds, in 100ns units.
	if p.CPUTicks != 3*1e7 {
		t.Errorf("CPUTicks = %d, want %d (user + kernel)", p.CPUTicks, uint64(3*1e7))
	}
	if p.WorkingSet != 64<<20 || p.Private != 32<<20 {
		t.Errorf("memory = %d/%d, want %d/%d", p.WorkingSet, p.Private, 64<<20, 32<<20)
	}
	if p.ReadBytes != 1000 || p.WriteBytes != 2000 {
		t.Errorf("io = %d/%d, want 1000/2000", p.ReadBytes, p.WriteBytes)
	}
	if p.Threads != 12 || p.SessionID != 1 {
		t.Errorf("threads/session = %d/%d, want 12/1", p.Threads, p.SessionID)
	}

	if got[2].Name != "notepad.exe" {
		t.Errorf("last entry name = %q, want notepad.exe", got[2].Name)
	}
}

// TestParseProcessesStopsOnAMalformedChain: the offsets come from the kernel, but
// this walks them with unsafe pointers, so a zero-length or backward step must end
// the walk rather than loop for ever or read past the buffer.
func TestParseProcessesStopsOnAMalformedChain(t *testing.T) {
	const size = int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))

	// An entry pointing at itself: the shape that would spin.
	selfRef := make([]byte, size*2)
	e := windows.SYSTEM_PROCESS_INFORMATION{UniqueProcessID: 7, NextEntryOffset: 0}
	*(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&selfRef[0])) = e
	// Rewrite the offset to zero *bytes forward*, which is not the terminator
	// value the walk checks first.
	(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&selfRef[0])).NextEntryOffset = 0
	if got := parseProcesses(selfRef); len(got) != 1 {
		t.Errorf("got %d entries from a single-entry buffer, want 1", len(got))
	}

	// An offset past the end of the buffer.
	overrun := make([]byte, size)
	*(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&overrun[0])) =
		windows.SYSTEM_PROCESS_INFORMATION{UniqueProcessID: 9, NextEntryOffset: uint32(size * 10)}
	if got := parseProcesses(overrun); len(got) != 1 {
		t.Errorf("got %d entries when the chain ran off the end, want 1", len(got))
	}

	// A buffer too short to hold even one entry.
	if got := parseProcesses(make([]byte, 8)); len(got) != 0 {
		t.Errorf("got %d entries from a truncated buffer, want 0", len(got))
	}
	if got := parseProcesses(nil); len(got) != 0 {
		t.Errorf("got %d entries from no buffer at all, want 0", len(got))
	}
}

// TestNTStringLengthIsInBytes is the mistake this file is most likely to contain,
// pinned on its own: NTUnicodeString.Length counts bytes, so the character count is
// half of it.
func TestNTStringLengthIsInBytes(t *testing.T) {
	utf16 := windows.StringToUTF16("chrome.exe")
	s := windows.NTUnicodeString{
		Length:        uint16((len(utf16) - 1) * 2),
		MaximumLength: uint16(len(utf16) * 2),
		Buffer:        &utf16[0],
	}
	if got := ntString(s); got != "chrome.exe" {
		t.Errorf("ntString = %q, want %q", got, "chrome.exe")
	}
	if got := ntString(windows.NTUnicodeString{}); got != "" {
		t.Errorf("an empty string gave %q", got)
	}
}
