package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVendoredAdwaitaIsGuarded holds third_party/gotk4-adwaita to the one change
// Atlas makes to it: no generated signal trampoline may panic when its closure
// has already been released. gotk4 0.4.1 releases closures during object
// cleanup, and a signal in that window reaching an unguarded trampoline would
// take the application down. See scripts/vendor-adwaita.sh.
func TestVendoredAdwaitaIsGuarded(t *testing.T) {
	root := filepath.Join("third_party", "gotk4-adwaita")
	if _, err := os.Stat(root); err != nil {
		t.Skip("gotk4-adwaita is not vendored")
	}
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gomod), "=> ./third_party/gotk4-adwaita") {
		t.Error("third_party/gotk4-adwaita exists but go.mod does not replace the module with it")
	}
	files, unguarded := 0, 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_export.go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		if n := strings.Count(string(b), `panic("given unknown closure user_data")`); n > 0 {
			unguarded += n
			t.Errorf("%s: %d unguarded trampolines — re-run scripts/vendor-adwaita.sh", path, n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no generated trampoline files found; has the layout changed?")
	}
	t.Logf("%d trampoline files, %d unguarded", files, unguarded)
}
