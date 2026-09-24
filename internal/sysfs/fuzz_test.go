package sysfs

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Every collector reads through this package, so its two parsers run on more
// bytes than anything else in the app.

// FuzzSysfsParseUint compares the digit scanner with strconv where the input is
// a plain integer, and checks it never panics otherwise.
func FuzzSysfsParseUint(f *testing.F) {
	f.Add([]byte("0"))
	f.Add([]byte("3400000"))
	f.Add([]byte("18446744073709551615"))
	f.Add([]byte("42\n"))
	f.Add([]byte(" 42"))
	f.Add([]byte("-42"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, b []byte) {
		got, ok := ParseUint(b)
		if !ok {
			// Rejected input must have no leading digit.
			if len(b) > 0 && b[0] >= '0' && b[0] <= '9' {
				t.Fatalf("ParseUint(%q) rejected input starting with a digit", b)
			}
			return
		}
		if want, err := strconv.ParseUint(string(b), 10, 64); err == nil && got != want {
			t.Fatalf("ParseUint(%q) = %d, strconv says %d", b, got, want)
		}
	})
}

// FuzzTrimSpace checks the trimmer returns a sub-slice of its input and leaves
// no leading or trailing whitespace behind.
func FuzzTrimSpace(f *testing.F) {
	f.Add([]byte(" x \n"))
	f.Add([]byte("\t\n\r "))
	f.Add([]byte(""))
	f.Add([]byte("x"))

	f.Fuzz(func(t *testing.T, b []byte) {
		got := TrimSpace(b)
		if len(got) > len(b) {
			t.Fatalf("TrimSpace grew the input")
		}
		if len(got) > 0 && !bytes.Contains(b, got) {
			t.Fatalf("result %q is not a slice of the input %q", got, b)
		}
		if n := len(got); n > 0 {
			if isSpace(got[0]) || isSpace(got[n-1]) {
				t.Fatalf("TrimSpace(%q) = %q still has edge whitespace", b, got)
			}
		}
	})
}

// FuzzFileContents writes arbitrary bytes into a file, reads it through a held
// descriptor, and checks the accessors agree with the file. This is the path
// every /proc and /sys read takes, including the buffer-growth case.
func FuzzFileContents(f *testing.F) {
	f.Add([]byte("42\n"), 8)
	f.Add([]byte(""), 8)
	f.Add([]byte("0123456789"), 4) // longer than the buffer
	f.Add([]byte("01234567"), 8)   // exactly the buffer
	f.Add(bytes.Repeat([]byte("x"), 100), 1)

	f.Fuzz(func(t *testing.T, content []byte, size int) {
		if size < 1 || size > 1<<16 {
			return
		}
		path := filepath.Join(t.TempDir(), "attr")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Skip(err)
		}
		fl := OpenSize(path, size)
		if fl == nil || !fl.OK() {
			t.Fatalf("OpenSize failed for a file that exists")
		}
		defer fl.Close()

		// Read twice: the second read must agree with the first, since the file
		// has not changed, and must exercise any buffer the first read grew.
		// An empty file reports failure by design — a sysfs attribute with no
		// bytes in it has no value to hand a caller.
		for i := 0; i < 2; i++ {
			got, ok := fl.Bytes()
			if len(content) == 0 {
				if ok {
					t.Fatalf("read %d of an empty file reported success", i)
				}
				continue
			}
			if !ok {
				t.Fatalf("read %d failed for %d bytes of content", i, len(content))
			}
			if !bytes.Equal(got, content) {
				t.Fatalf("read %d returned %q, file holds %q (buffer %d)", i, got, content, size)
			}
		}
		// Uint must match ParseUint of the trimmed contents.
		want, wantOK := ParseUint(TrimSpace(content))
		got, gotOK := fl.Uint()
		if gotOK != wantOK || (wantOK && got != want) {
			t.Fatalf("Uint() = (%d,%v), want (%d,%v) for %q", got, gotOK, want, wantOK, content)
		}
	})
}
