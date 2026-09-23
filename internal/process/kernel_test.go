package process

import (
	"os"
	"strconv"
	"testing"
)

// TestKernelThreadDetection checks the PF_KTHREAD read against this machine's
// live /proc.
//
// It cannot simply assert that kernel threads exist: inside a container the PID
// namespace shows only the container's own processes and there are none. So the
// invariants checked here are the ones that hold everywhere — pid 1 is never a
// kernel thread, and anything flagged as one must have an empty command line,
// which is the independent ground truth for what a kernel thread is.
func TestKernelThreadDetection(t *testing.T) {
	c := New()
	c.collect()
	snap := c.Snapshot()
	if len(snap) == 0 {
		t.Skip("no processes readable")
	}

	var kernel, user int
	var kernelNames, userNames []string
	for _, p := range snap {
		if p.Kernel {
			kernel++
			if len(kernelNames) < 5 {
				kernelNames = append(kernelNames, p.Name)
			}
			continue
		}
		user++
		if len(userNames) < 5 {
			userNames = append(userNames, p.Name)
		}
	}
	t.Logf("%d processes: %d kernel threads, %d userspace", len(snap), kernel, user)
	t.Logf("kernel:    %v", kernelNames)
	t.Logf("userspace: %v", userNames)

	if user == 0 {
		t.Fatal("every process was classed as a kernel thread")
	}

	for _, p := range snap {
		// pid 1 is init — the container's or the machine's, but always a program.
		if p.PID == 1 && p.Kernel {
			t.Errorf("pid 1 (%s) classed as a kernel thread", p.Name)
		}
		// kthreadd, when it is visible, is the archetypal kernel thread.
		if p.PID == 2 && p.Name == "kthreadd" && !p.Kernel {
			t.Error("pid 2 (kthreadd) not classed as a kernel thread")
		}
		// A kernel thread has no command line. The converse does not hold — a
		// zombie program has none either — so this is checked one way only.
		if p.Kernel && hasCmdline(p.PID) {
			t.Errorf("%s (pid %d) is flagged as a kernel thread but has a command line",
				p.Name, p.PID)
		}
	}

	if kernel == 0 {
		t.Log("no kernel threads visible — this is a container PID namespace, " +
			"so only the flag's correctness on userspace processes was checked")
	}
}

// hasCmdline reports whether a process has a non-empty /proc/<pid>/cmdline.
func hasCmdline(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return err == nil && len(b) > 0
}
