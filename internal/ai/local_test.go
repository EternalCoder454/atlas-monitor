package ai

import "testing"

// TestIsLocal pins which endpoints count as "nothing leaves the machine". The
// assistant's system prompt carries the hostname, the username and the process
// list, and this client cannot speak TLS, so the distinction decides whether the
// Settings dialog warns.
func TestIsLocal(t *testing.T) {
	local := []string{
		"http://localhost:11434",
		"localhost:11434",
		"localhost",
		"http://127.0.0.1:11434",
		"127.0.0.1",
		"http://127.4.5.6:11434",
		"http://[::1]:11434",
		"::1",
		"http://dev.localhost:11434",
		"", // unconfigured: nothing is sent
		"  http://localhost:11434/  ",
	}
	remote := []string{
		"http://192.168.1.50:11434",
		"192.168.1.50",
		"http://10.0.0.9:11434",
		"http://ollama.example.com:11434",
		"ollama.example.com",
		"http://[2001:db8::1]:11434",
		"http://0.0.0.0:11434", // reachable from off-box
	}
	for _, u := range local {
		if !IsLocal(u) {
			t.Errorf("IsLocal(%q) = false, want true", u)
		}
	}
	for _, u := range remote {
		if IsLocal(u) {
			t.Errorf("IsLocal(%q) = true, want false — the snapshot would leave the machine in the clear", u)
		}
	}
}
