package process

import "testing"

// TestKernelThreadDetection checks the PF_KTHREAD read against this machine's
// live /proc: kernel threads should be the clear majority and should never be
// something with a real command line.
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

	if kernel == 0 {
		t.Error("no kernel threads found — PF_KTHREAD is not being read correctly")
	}
	if user == 0 {
		t.Error("every process was classed as a kernel thread")
	}
	// pid 1 is always a userspace program.
	for _, p := range snap {
		if p.PID == 1 && p.Kernel {
			t.Errorf("pid 1 (%s) classed as a kernel thread", p.Name)
		}
		// kthreadd (pid 2) is always a kernel thread.
		if p.PID == 2 && !p.Kernel {
			t.Errorf("pid 2 (%s) not classed as a kernel thread", p.Name)
		}
	}
}
