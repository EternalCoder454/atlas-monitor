package process

import (
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

// TestDetailsOfThisProcess checks every field against what the test process
// knows about itself.
func TestDetailsOfThisProcess(t *testing.T) {
	in, err := Details(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if in.PID != os.Getpid() || in.PPID != os.Getppid() {
		t.Errorf("PID/PPID = %d/%d, want %d/%d", in.PID, in.PPID, os.Getpid(), os.Getppid())
	}
	if exe, _ := os.Executable(); in.Exe != exe {
		t.Errorf("Exe = %q, want %q", in.Exe, exe)
	}
	if !reflect.DeepEqual(in.Cmdline, os.Args) {
		t.Errorf("Cmdline = %q, want %q", in.Cmdline, os.Args)
	}
	if in.Threads < 1 || in.RSS == 0 || in.User == "" || in.State == "" || in.ParentName == "" {
		t.Errorf("missing fields: %+v", in)
	}
	if age := time.Since(in.Started); age < 0 || age > time.Hour {
		t.Errorf("Started = %v, %v ago; this process started moments ago", in.Started, age)
	}
	// Our own process is always readable in full.
	if !in.HaveSmaps || in.PSS == 0 || in.Private == 0 || in.PSS > in.RSS {
		t.Errorf("smaps: have %v, PSS %d, Private %d, RSS %d", in.HaveSmaps, in.PSS, in.Private, in.RSS)
	}
	if !in.HaveFiles || in.OpenFiles < 3 {
		t.Errorf("open files: have %v, %d; stdin, stdout and stderr at least", in.HaveFiles, in.OpenFiles)
	}
	if !in.HaveNice {
		t.Error("nice not read")
	}
	t.Logf("%+v", in)
}

func TestDetailsOfAProcessThatIsGone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot run true:", err)
	}
	if _, err := Details(cmd.Process.Pid); err == nil {
		t.Error("no error for a process that has exited")
	}
}

func TestSplitCmdline(t *testing.T) {
	cases := map[string][]string{
		"":                              nil,
		"\x00":                          nil,
		"/usr/bin/foo\x00-a\x00b c\x00": {"/usr/bin/foo", "-a", "b c"},
		"electron --type=renderer":      {"electron --type=renderer"}, // rewritten in place, no NULs
	}
	for in, want := range cases {
		if got := splitCmdline([]byte(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("splitCmdline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKBField(t *testing.T) {
	// smaps_rollup's shape, with SwapPss moved ahead of Pss: a key must match
	// only where a line starts.
	b := []byte("55b4c7743000-7ffe005dd000 ---p 00000000 00:00 0    [rollup]\n" +
		"Rss:              1234 kB\nSwapPss:            99 kB\nPss:               567 kB\nPrivate_Dirty:      8 kB\n")
	if got := kBField(b, "Pss:"); got != 567*1024 {
		t.Errorf("Pss = %d", got)
	}
	if got := kBField(b, "Rss:"); got != 1234*1024 {
		t.Errorf("Rss = %d", got)
	}
	if got := kBField(b, "Swap:"); got != 0 {
		t.Errorf("absent field = %d, want 0", got)
	}
	if got := kBField([]byte("Rss: 4 kB\n"), "Rss:"); got != 4*1024 {
		t.Errorf("key at the very start = %d", got)
	}
}
