package process

import "testing"

// TestProcessNameDropsTheExtension: every row in the Apps table would otherwise
// end in ".exe", which is four characters of nothing in a column that has to be
// narrow. Only that extension is dropped — a program with a dot in its name keeps
// it.
func TestProcessNameDropsTheExtension(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"firefox.exe", "firefox"},
		{"Code.exe", "Code"},
		{"POWERPNT.EXE", "POWERPNT"}, // the case Windows actually reports it in
		{"node.js.exe", "node.js"},   // only the last extension goes
		{"python3.11", "python3.11"}, // not an .exe: left alone
		{"svchost", "svchost"},
		{".exe", ".exe"}, // nothing before the dot is not a name
	} {
		got := processName(winProc(c.in, 100))
		if got != c.want {
			t.Errorf("processName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestProcessNameFallsBackToThePID: a protected or exiting process will not give
// up its name, and a blank row is worse than an ugly one.
func TestProcessNameFallsBackToThePID(t *testing.T) {
	got := processName(winProc("", 4242))
	if got != "(pid 4242)" {
		t.Errorf("got %q, want %q", got, "(pid 4242)")
	}
}

func TestItoa(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{{0, "0"}, {4, "4"}, {42, "42"}, {4242, "4242"}, {1000000, "1000000"}} {
		if got := itoa(c.in); got != c.want {
			t.Errorf("itoa(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
