package process

import "time"

// Info is everything the Details panel shows about one process: who started it,
// what it is running, and what it is holding on to.
//
// It is read on demand, when somebody asks, rather than by the scan — several of
// these fields cost a file each per process, and nobody needs them for seven
// hundred processes a second. A field that could not be read is left at its zero
// value, and the Have flags say which of the optional ones were.
type Info struct {
	PID        int
	Name       string
	PPID       int
	ParentName string
	Exe        string
	// Cmdline is the arguments as given, the program first. Empty where the
	// platform will not say.
	Cmdline []string
	User    string
	Started time.Time
	// State is the scheduler's word for what the process is doing: running,
	// sleeping, stopped. Empty where the platform has no such thing.
	State    string
	Threads  int
	Nice     int
	HaveNice bool
	CPUTime  time.Duration

	RSS  uint64
	Swap uint64
	// PSS is the resident size with shared pages divided among the processes
	// that share them, and Private the pages that are this process's alone: the
	// two honest answers to "how much memory is this using". Reading them needs
	// the same permission as tracing the process, so another user's process has
	// neither; HaveSmaps says whether they were read.
	PSS, Private uint64
	HaveSmaps    bool

	OpenFiles int
	HaveFiles bool

	Unit string
}
