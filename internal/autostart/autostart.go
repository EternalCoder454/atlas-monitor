// Package autostart reads and edits the programs that start when you log in.
//
// These are XDG desktop entries: files in /etc/xdg/autostart that the system
// installs, and files in ~/.config/autostart that belong to the user. A user
// file shadows a system file of the same name, which is the mechanism for
// switching one off — a copy with Hidden=true, which the specification says
// must be ignored at login.
//
// Most of what is in the system directory is desktop plumbing that nobody
// should be turning off: twenty-two of the twenty-five entries on the machine
// this was written on are marked NoDisplay, which is the freedesktop way of
// saying "not for a settings window". Those are kept back unless asked for, the
// same way the process table keeps back kernel threads.
package autostart

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one program that starts at login.
type Entry struct {
	Name    string // human name, from the desktop entry
	Comment string
	Exec    string // the command line, as written
	File    string // the file this came from

	// System is true for an entry installed by the system rather than the user.
	System bool
	// Plumbing is the entry's own NoDisplay flag: it does not want to be listed
	// in a settings window.
	Plumbing bool
	// Disabled is Hidden=true — switched off, by us or by something else.
	Disabled bool
	// RunsHere is false when OnlyShowIn or NotShowIn rules this entry out of the
	// current desktop, so it is listed as something that will not actually run.
	RunsHere bool

	// hasName records whether the file actually carried a Name. A file written
	// only to switch something off carries nothing else, and the list should
	// still show the program's real name rather than its filename.
	hasName bool
	// systemFile is the packaged file this entry shadows, if any. Switching the
	// entry back on removes the override and hands it back, rather than leaving
	// a copy behind to shadow every future update of it.
	systemFile string
}

// Reader looks in a set of directories. The zero value is not useful; use New.
type Reader struct {
	systemDirs []string
	userDir    string
	desktop    []string // XDG_CURRENT_DESKTOP, split
}

// New reads the standard locations for the current desktop.
func New() *Reader {
	return NewAt(systemDirs(), userDir(), strings.Split(os.Getenv("XDG_CURRENT_DESKTOP"), ":"))
}

// NewAt is New with the locations given, for tests.
func NewAt(systemDirs []string, userDir string, desktop []string) *Reader {
	return &Reader{systemDirs: systemDirs, userDir: userDir, desktop: desktop}
}

func systemDirs() []string {
	raw := os.Getenv("XDG_CONFIG_DIRS")
	if raw == "" {
		raw = "/etc/xdg"
	}
	var out []string
	for _, d := range strings.Split(raw, ":") {
		if d != "" {
			out = append(out, filepath.Join(d, "autostart"))
		}
	}
	return out
}

func userDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "autostart")
}

// List returns every entry, user files shadowing system files of the same name,
// sorted by the name people read.
func (r *Reader) List() []Entry {
	byBase := map[string]Entry{}
	for _, dir := range r.systemDirs {
		for base, e := range r.readDir(dir, true) {
			byBase[base] = e
		}
	}
	// The user's own directory wins: that is how switching one off works.
	for base, e := range r.readDir(r.userDir, false) {
		if sys, ok := byBase[base]; ok {
			// A file written only to disable a system entry carries nothing
			// worth reading, so keep the system entry's description and take
			// just its state.
			if !e.hasName {
				e.Name, e.Comment, e.Exec = sys.Name, sys.Comment, sys.Exec
				e.hasName = sys.hasName
			}
			e.Plumbing = sys.Plumbing
			e.systemFile = sys.File
		}
		byBase[base] = e
	}

	out := make([]Entry, 0, len(byBase))
	for base, e := range byBase {
		if !e.hasName {
			e.Name = strings.TrimSuffix(base, ".desktop")
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (r *Reader) readDir(dir string, system bool) map[string]Entry {
	out := map[string]Entry{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".desktop") {
			continue
		}
		e, ok := r.parse(filepath.Join(dir, f.Name()))
		if !ok {
			continue
		}
		e.System = system
		out[f.Name()] = e
	}
	return out
}

// parse reads the [Desktop Entry] group. Desktop files are INI-shaped; only the
// handful of keys that decide what is shown and whether it runs are read.
func (r *Reader) parse(path string) (Entry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer f.Close()

	e := Entry{File: path, RunsHere: true}
	var onlyShowIn, notShowIn []string
	inGroup := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inGroup = line == "[Desktop Entry]"
			continue
		}
		if !inGroup {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Name":
			e.Name, e.hasName = value, true
		case "Comment":
			e.Comment = value
		case "Exec":
			e.Exec = value
		case "NoDisplay":
			e.Plumbing = value == "true"
		case "Hidden":
			e.Disabled = value == "true"
		case "OnlyShowIn":
			onlyShowIn = splitList(value)
		case "NotShowIn":
			notShowIn = splitList(value)
		}
	}
	e.RunsHere = r.runsHere(onlyShowIn, notShowIn)
	return e, true
}

func (r *Reader) runsHere(only, not []string) bool {
	for _, d := range not {
		if r.isDesktop(d) {
			return false
		}
	}
	if len(only) == 0 {
		return true
	}
	for _, d := range only {
		if r.isDesktop(d) {
			return true
		}
	}
	return false
}

func (r *Reader) isDesktop(name string) bool {
	for _, d := range r.desktop {
		if strings.EqualFold(strings.TrimSpace(d), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ";") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SetEnabled switches an entry on or off.
//
// Off is a copy in the user's directory with Hidden=true, which is what the
// specification says to honour and what every desktop's own settings window
// writes. The system's file is never touched: it belongs to a package, an
// update would put it back, and editing it needs root for something the user
// is entitled to decide.
func SetEnabled(e Entry, on bool) error {
	return setEnabledAt(e, on, userDir())
}

func setEnabledAt(e Entry, on bool, userDir string) error {
	target := filepath.Join(userDir, filepath.Base(e.File))

	if on {
		// An entry that shadows a packaged one goes back to it; one the user
		// owns outright just loses its Hidden key.
		if e.System || e.systemFile != "" {
			// The override exists only to disable it; removing it hands the
			// entry back to the system file unchanged.
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		return rewriteHidden(e.File, false)
	}

	if !e.System {
		return rewriteHidden(e.File, true)
	}
	body, err := os.ReadFile(e.File)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, withHidden(body, true), 0o644); err != nil {
		return err
	}
	return nil
}

// rewriteHidden sets or clears Hidden= in a file the user owns.
func rewriteHidden(path string, hidden bool) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, withHidden(body, hidden), 0o644)
}

// withHidden returns body with Hidden set as asked, replacing an existing key
// rather than adding a second one.
func withHidden(body []byte, hidden bool) []byte {
	want := "Hidden=false"
	if hidden {
		want = "Hidden=true"
	}
	lines := strings.Split(string(body), "\n")
	inGroup, done := false, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if inGroup && !done {
				// Leaving the group without having seen the key: add it last.
				lines[i] = want + "\n" + line
				done = true
			}
			inGroup = trimmed == "[Desktop Entry]"
			continue
		}
		if inGroup && strings.HasPrefix(trimmed, "Hidden=") {
			lines[i] = want
			done = true
		}
	}
	if !done {
		out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
		return []byte(out + "\n" + want + "\n")
	}
	return []byte(strings.Join(lines, "\n"))
}
