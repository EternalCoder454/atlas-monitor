package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The icons are the one part of Atlas whose breakage is invisible until someone
// changes theme. GTK recolours a symbolic icon by forcing the fill on rect,
// circle and path, so stroked artwork keeps whatever colour it was drawn in —
// fine on a light background, black on black in dark mode. A missing file is
// just as quiet: GTK falls back to a generic "image missing" glyph rather than
// failing. These tests hold both cases down.

const iconDir = "assets/icons"

// iconNameRE matches the icon names the code asks GTK for.
var iconNameRE = regexp.MustCompile(`"(atlas-[a-z0-9-]+-symbolic)"`)

// commentRE strips XML comments before the artwork is inspected. The generated
// files carry a header explaining why strokes are not allowed, and searching the
// raw text would match that explanation rather than any actual stroke.
var commentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

// strokeRE matches a stroke used as an attribute or a CSS property — stroke=,
// stroke:, stroke-width= and so on — rather than the word in prose.
var strokeRE = regexp.MustCompile(`\bstroke(-[a-z]+)?\s*[:=]`)

// iconsReferenced scans the Go sources for every icon name the app uses.
func iconsReferenced(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	err := filepath.Walk("internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range iconNameRE.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning sources: %v", err)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// iconFiles lists the installed-name icons on disk.
func iconFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(iconDir, "atlas-*-symbolic.svg"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, strings.TrimSuffix(filepath.Base(p), ".svg"))
	}
	sort.Strings(out)
	return out
}

// TestEveryIconTheCodeAsksForExists catches the case where a new icon is wired
// up but its file never arrives. GTK shows a broken-image glyph for that rather
// than complaining, so nothing else would notice.
func TestEveryIconTheCodeAsksForExists(t *testing.T) {
	have := map[string]bool{}
	for _, name := range iconFiles(t) {
		have[name] = true
	}
	refs := iconsReferenced(t)
	if len(refs) == 0 {
		t.Fatal("found no icon references at all; the scan is broken, not the icons")
	}
	for _, name := range refs {
		if !have[name] {
			t.Errorf("the code asks for %q but %s/%s.svg does not exist", name, iconDir, name)
		}
	}
	t.Logf("%d icons referenced, all present", len(refs))
}

// TestIconsAreRecolourable is the dark-mode guard. Anything stroked, or any
// element explicitly filled "none", stops behaving once GTK applies the theme
// colour — the first keeps its own colour, the second turns into a solid block.
func TestIconsAreRecolourable(t *testing.T) {
	names := iconFiles(t)
	if len(names) == 0 {
		t.Fatal("no icons found")
	}
	for _, name := range names {
		path := filepath.Join(iconDir, name+".svg")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		src := commentRE.ReplaceAllString(string(body), "")
		if m := strokeRE.FindString(src); m != "" {
			t.Errorf("%s uses %q: a stroke keeps its own colour and vanishes in dark mode", name, strings.TrimSpace(m))
		}
		if strings.Contains(src, `fill="none"`) {
			t.Errorf(`%s has a fill="none" element: GTK forces the fill, so it becomes a solid block`, name)
		}
		// Gradients cannot be recoloured either.
		if strings.Contains(src, "linearGradient") || strings.Contains(src, "radialGradient") {
			t.Errorf("%s uses a gradient, which a symbolic icon cannot have", name)
		}
		if err := xml.Unmarshal(body, new(struct {
			XMLName xml.Name
		})); err != nil {
			t.Errorf("%s is not well-formed XML: %v", name, err)
		}
	}
	t.Logf("%d icons checked", len(names))
}

// TestPackagingInstallsEveryIcon checks the Makefile's list against the files.
// An icon that exists and is referenced but never installed is the same as a
// missing one at runtime, and this is exactly the kind of list that gets
// forgotten when a new icon is added.
func TestPackagingInstallsEveryIcon(t *testing.T) {
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Skipf("cannot read Makefile: %v", err)
	}
	var listed []string
	for _, line := range strings.Split(string(mk), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ICONS"); ok {
			if _, v, found := strings.Cut(rest, "="); found {
				listed = strings.Fields(v)
			}
			break
		}
	}
	if len(listed) == 0 {
		t.Fatal("no ICONS list found in the Makefile")
	}
	installed := map[string]bool{}
	for _, short := range listed {
		installed["atlas-"+short+"-symbolic"] = true
	}
	for _, name := range iconFiles(t) {
		if !installed[name] {
			t.Errorf("%s.svg exists but the Makefile's ICONS list does not install it", name)
		}
	}
	for short := range installed {
		if _, err := os.Stat(filepath.Join(iconDir, short+".svg")); err != nil {
			t.Errorf("the Makefile installs %s but there is no such file", short)
		}
	}
	t.Logf("%d icons installed by the Makefile", len(listed))
}

// iconListIn pulls the icon names out of one of the places that installs them.
// Each packaging format spells the same list its own way, and none of them
// notices when it falls behind: a missing entry installs nothing and the app
// draws a broken image where that icon should be.
func iconListIn(t *testing.T, path, start, end string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}
	body := string(b)
	i := strings.Index(body, start)
	if i < 0 {
		t.Fatalf("%s: no %q to read the icon list from", path, start)
	}
	body = body[i+len(start):]
	if j := strings.Index(body, end); j >= 0 {
		body = body[:j]
	}
	var out []string
	for _, f := range strings.Fields(body) {
		if f == "\\" { // a line continuation, not an icon
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// TestPackagingListsAgree keeps the three installers in step. The Makefile is
// the one the project itself uses, so it is treated as the source of truth; the
// RPM spec and the Arch PKGBUILD have to match it.
func TestPackagingListsAgree(t *testing.T) {
	want := iconListIn(t, "Makefile", "ICONS   :=", "\n")
	if len(want) == 0 {
		t.Fatal("no ICONS list found in the Makefile")
	}
	for _, c := range []struct{ path, start, end string }{
		{"packaging/atlas-monitor.spec", "for icon in", ";"},
		{"packaging/PKGBUILD", "_icons=(", ")"},
	} {
		got := iconListIn(t, c.path, c.start, c.end)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s installs a different set of icons than the Makefile\n  it has:   %s\n  Makefile: %s",
				c.path, strings.Join(got, " "), strings.Join(want, " "))
		}
	}
	t.Logf("%d icons, listed the same way in all three installers", len(want))
}
