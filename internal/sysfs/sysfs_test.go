package sysfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUintAndBytes(t *testing.T) {
	dir := t.TempDir()
	f := Open(write(t, dir, "value", "3456789\n"))
	defer f.Close()
	if !f.OK() {
		t.Fatal("file did not open")
	}
	for i := 0; i < 3; i++ { // the point is repeated reads from one descriptor
		v, ok := f.Uint()
		if !ok || v != 3456789 {
			t.Fatalf("read %d: Uint = %d, %v; want 3456789", i, v, ok)
		}
	}
	b, ok := f.Bytes()
	if !ok || strings.TrimSpace(string(b)) != "3456789" {
		t.Errorf("Bytes = %q, %v", b, ok)
	}
}

// TestRereadsChangedContent is the behaviour the collectors depend on: kernel
// files are regenerated on each read, so a held descriptor must see new values.
func TestRereadsChangedContent(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "counter", "1\n")
	f := Open(path)
	defer f.Close()

	if v, _ := f.Uint(); v != 1 {
		t.Fatalf("first read = %d, want 1", v)
	}
	write(t, dir, "counter", "42\n")
	if v, _ := f.Uint(); v != 42 {
		t.Errorf("after rewrite = %d, want 42 — the descriptor is not re-reading", v)
	}
}

// TestGrowsForLargeFiles: a file bigger than the buffer must never come back
// truncated, because a half-read /proc/stat would silently corrupt every figure.
func TestGrowsForLargeFiles(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("0123456789abcdef", 4096) // 64 KiB, far past the default
	f := Open(write(t, dir, "big", big))
	defer f.Close()

	b, ok := f.Bytes()
	if !ok {
		t.Fatal("large file did not read")
	}
	if len(b) != len(big) {
		t.Fatalf("read %d bytes, want %d — the buffer did not grow", len(b), len(big))
	}
	if string(b) != big {
		t.Error("content does not match")
	}
}

// TestExactBufferBoundary covers the case where the content is exactly the
// buffer size, which is indistinguishable from a truncated read.
func TestExactBufferBoundary(t *testing.T) {
	dir := t.TempDir()
	exact := strings.Repeat("x", smallValue)
	f := Open(write(t, dir, "exact", exact))
	defer f.Close()
	b, ok := f.Bytes()
	if !ok || len(b) != len(exact) {
		t.Fatalf("read %d bytes, want %d", len(b), len(exact))
	}
}

func TestMissingFileIsSafe(t *testing.T) {
	f := Open(filepath.Join(t.TempDir(), "absent"))
	if f != nil {
		t.Fatal("Open of a missing file should return nil")
	}
	// Every method must tolerate the nil the collectors will hold.
	if f.OK() {
		t.Error("nil File reports OK")
	}
	if _, ok := f.Uint(); ok {
		t.Error("nil File returned a value")
	}
	if _, ok := f.Bytes(); ok {
		t.Error("nil File returned bytes")
	}
	f.Close() // must not panic
}

func TestOpenFirst(t *testing.T) {
	dir := t.TempDir()
	second := write(t, dir, "second", "7\n")
	f := OpenFirst(filepath.Join(dir, "absent"), second)
	defer f.Close()
	if v, ok := f.Uint(); !ok || v != 7 {
		t.Errorf("OpenFirst picked the wrong file: %d, %v", v, ok)
	}
	if OpenFirst(filepath.Join(dir, "no"), filepath.Join(dir, "nope")) != nil {
		t.Error("OpenFirst should return nil when nothing exists")
	}
}

func TestParseUint(t *testing.T) {
	for in, want := range map[string]uint64{
		"0\n": 0, "42": 42, "18446744073709551615": 18446744073709551615,
		"123 456": 123, "99abc": 99,
	} {
		if v, ok := ParseUint([]byte(in)); !ok || v != want {
			t.Errorf("ParseUint(%q) = %d, %v; want %d", in, v, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "\n", " 5"} {
		if _, ok := ParseUint([]byte(in)); ok {
			t.Errorf("ParseUint(%q) should report no digits", in)
		}
	}
}

// BenchmarkHeldOpen is the reason this package exists: compare re-reading a
// held descriptor against os.ReadFile, which is what the collectors used to do
// for every attribute on every tick.
func BenchmarkHeldOpen(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "value")
	os.WriteFile(path, []byte("3456789\n"), 0o644)
	f := Open(path)
	defer f.Close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.Uint()
	}
}

func BenchmarkReadFile(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "value")
	os.WriteFile(path, []byte("3456789\n"), 0o644)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ReadUint(path)
	}
}
