package gpu

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// The GPU figures come from DRM fdinfo and from the PCI ID database, both parsed
// as bytes. fdinfo in particular is written by whichever driver is loaded, and
// its format has changed between kernel releases, so the parsers have to hold up
// against text they were not written for.

// FuzzCutKey feeds arbitrary bytes to the fdinfo key lookup.
func FuzzCutKey(f *testing.F) {
	f.Add([]byte("drm-client-id:\t42\ndrm-engine-gfx:\t12345 ns\n"), "drm-client-id:")
	f.Add([]byte("drm-client-id:"), "drm-client-id:")
	f.Add([]byte("drm-client-id:\n"), "drm-client-id:")
	f.Add([]byte(""), "")
	f.Add([]byte("x"), "")

	f.Fuzz(func(t *testing.T, data []byte, key string) {
		got, ok := cutKey(data, []byte(key))
		if !ok {
			return
		}
		if len(got) > len(data) {
			t.Fatalf("result longer than input")
		}
		// The result must be text that was in the input.
		if len(got) > 0 && !bytes.Contains(data, got) {
			t.Fatalf("result %q is not a slice of the input %q", got, data)
		}
	})
}

// FuzzGPUParseUint compares the fdinfo digit scanner with strconv.
func FuzzGPUParseUint(f *testing.F) {
	f.Add([]byte("12345"))
	f.Add([]byte("12345 ns"))
	f.Add([]byte(""))
	f.Add([]byte("ns"))

	f.Fuzz(func(t *testing.T, b []byte) {
		got := parseUint(b)
		if want, err := strconv.ParseUint(string(b), 10, 64); err == nil && got != want {
			t.Fatalf("parseUint(%q) = %d, strconv says %d", b, got, want)
		}
	})
}

// FuzzScanPCIIDs feeds arbitrary text to the pci.ids scanner used to name the
// card. The file is 1 MB of third-party data from hwdata, so it is worth knowing
// a mangled copy cannot bring the app down or yield a nonsense name.
func FuzzScanPCIIDs(f *testing.F) {
	f.Add("1002  Advanced Micro Devices, Inc. [AMD/ATI]\n\t744c  Navi 31 [Radeon RX 7900 XT]\n", "1002", "744c")
	f.Add("", "1002", "744c")
	f.Add("\t\t\n", "", "")
	f.Add("1002\n\t744c\n", "1002", "744c")
	f.Add("1002  Vendor\n\t744c", "1002", "744c")

	f.Fuzz(func(t *testing.T, text, vendor, device string) {
		got := scanPCIIDs(strings.NewReader(text), vendor, device)
		if got == "" {
			return
		}
		// Whatever comes back has to be text from the file, not a construction.
		if !strings.Contains(text, got) {
			t.Fatalf("name %q was not in the input", got)
		}
	})
}

// FuzzVendorLabel checks the vendor-ID fallback table.
func FuzzVendorLabel(f *testing.F) {
	f.Add("1002")
	f.Add("0x1002")
	f.Add("10de")
	f.Add("8086")
	f.Add("")
	f.Add("zzzz")

	f.Fuzz(func(t *testing.T, vendor string) {
		_ = vendorLabel(vendor)
	})
}

// FuzzFdinfoEngineParse drives the engine-counter loop of readFdinfo over
// arbitrary text. readFdinfo reads a file, so the loop is reproduced here with
// the same byte operations; what is being checked is that no shape of input
// makes the line walk fail to terminate or index out of range.
func FuzzFdinfoEngineParse(f *testing.F) {
	f.Add([]byte("drm-engine-gfx:\t123 ns\ndrm-engine-compute:\t456 ns\n"))
	f.Add([]byte("drm-engine-:\t1\n"))
	f.Add([]byte("drm-engine-gfx\n"))
	f.Add([]byte("drm-engine-gfx:"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		engines := make(map[string]uint64)
		limit := len(data) + 2 // one iteration per byte at most, plus slack
		for i := 0; len(data) > 0; i++ {
			if i > limit {
				t.Fatal("line walk did not terminate")
			}
			var line []byte
			if j := bytes.IndexByte(data, '\n'); j >= 0 {
				line, data = data[:j], data[j+1:]
			} else {
				line, data = data, nil
			}
			rest, ok := bytes.CutPrefix(line, enginePrefix)
			if !ok {
				continue
			}
			colon := bytes.IndexByte(rest, ':')
			if colon < 0 {
				continue
			}
			name := string(bytes.TrimSpace(rest[:colon]))
			engines[name] += parseUint(bytes.TrimSpace(rest[colon+1:]))
		}
	})
}
