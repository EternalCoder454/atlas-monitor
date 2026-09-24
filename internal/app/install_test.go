package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRestartHelperTreatsPathsAsData is the regression for a shell injection in
// the updater. The helper used to be assembled with fmt.Sprintf("%q"), which is
// Go quoting rather than shell quoting: $(...) and backticks survive it, and the
// shell expands both inside double quotes. The script path comes from a file in
// the user's data directory, so this needed write access to the home directory
// to exploit — but a legitimate path containing '$' broke updating too.
func TestRestartHelperTreatsPathsAsData(t *testing.T) {
	dir := t.TempDir()
	proof := filepath.Join(dir, "INJECTED")

	// A "script path" of the shape that used to execute.
	evil := `/nonexistent/$(touch ` + proof + `)/update.sh`

	// Exactly the invocation onRestart makes, minus the relaunch.
	const helper = `if [ -n "$1" ]; then bash "$1" "$2"; fi; true`
	cmd := exec.Command("bash", "-c", helper, "atlas-monitor-restart", evil, "main")
	_ = cmd.Run() // bash will fail to find the script; that is fine

	if _, err := os.Stat(proof); err == nil {
		t.Fatal("the path was executed as shell syntax: the helper is injectable")
	}
}

// TestRestartHelperSkipsTheUpdateWhenThereIsNoScript checks the no-checkout
// case still relaunches rather than running "bash ”".
func TestRestartHelperSkipsTheUpdateWhenThereIsNoScript(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "RELAUNCHED")
	helper := `if [ -n "$1" ]; then bash "$1" "$2"; fi; touch "$3"`
	cmd := exec.Command("bash", "-c", helper, "atlas-monitor-restart", "", "main", marker)
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper failed with no script: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the relaunch step did not run when there was no update script")
	}
}
