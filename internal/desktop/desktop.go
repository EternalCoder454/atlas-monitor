// Package desktop says which application a process belongs to, and what that
// application is called.
//
// Grouping processes by their own names gets multi-process programs wrong:
// Firefox is "firefox", "Isolated Web Co", "WebExtensions" and a dozen more, and
// its memory is spread across rows that do not look related. The desktop
// already knows better. GNOME and KDE start every application in a systemd unit
// of its own, and name the unit after the application's desktop ID, which is
// also the name of its .desktop file — where the name people know it by and its
// icon are kept. So: unit name → application ID → desktop entry.
//
// The unit naming is systemd's convention for desktop environments
// (https://systemd.io/DESKTOP_ENVIRONMENTS/):
//
//	app[-<launcher>]-<ApplicationID>[@<RANDOM>].service
//	app[-<launcher>]-<ApplicationID>-<RANDOM>.scope
//
// A dash inside an ID is escaped as \x2d, so the literal dashes are the
// separators.
package desktop

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AppID returns the application ID encoded in a systemd unit name, and whether
// the unit is an application's at all. Services, sessions and the D-Bus
// activated helpers that share app.slice are not.
func AppID(unit string) (string, bool) {
	rest, ok := strings.CutPrefix(unit, "app-")
	if !ok {
		return "", false
	}
	scope := false
	switch {
	case strings.HasSuffix(rest, ".scope"):
		rest, scope = strings.TrimSuffix(rest, ".scope"), true
	case strings.HasSuffix(rest, ".service"):
		rest = strings.TrimSuffix(rest, ".service")
		if i := strings.IndexByte(rest, '@'); i >= 0 {
			rest = rest[:i] // the instance: a random string, or "autostart"
		}
	default:
		return "", false // a slice, or something else that holds no processes
	}
	parts := strings.Split(rest, "-")
	if scope && len(parts) > 1 {
		parts = parts[:len(parts)-1] // the random suffix
	}
	// What is left is the ID, with a launcher in front of it when there is one.
	id := unescape(parts[len(parts)-1])
	if id == "" {
		return "", false
	}
	return id, true
}

// unescape reverses systemd's \xNN escaping of unit names.
func unescape(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Entry is what a desktop file says about an application.
type Entry struct {
	ID   string
	Name string
	// Icon is an icon name to look up in the theme, or an absolute path.
	Icon string
	// Terminal is set for terminal emulators. A terminal busy with a build is
	// a job someone started and is waiting on, so Energy Saver leaves them be.
	Terminal bool
}

// FallbackName makes a readable name from an ID with no desktop file: the last
// dotted component of a reverse-DNS ID ("com.discordapp.Discord" → "Discord"),
// or the ID itself.
func FallbackName(id string) string {
	if i := strings.LastIndexByte(id, '.'); i >= 0 && i+1 < len(id) && strings.Count(id, ".") >= 2 {
		return id[i+1:]
	}
	return id
}

// Index finds desktop entries by ID. It lists the application directories once
// and parses a file only when its ID is first asked about.
type Index struct {
	mu      sync.Mutex
	dirs    []string // where to look; see DataDirs
	paths   map[string]string
	entries map[string]*Entry
	scanned time.Time
	now     func() time.Time
}

// NewIndex returns an index over dirs, each of which is a data directory with an
// applications/ folder in it. Earlier directories win, as XDG specifies.
func NewIndex(dirs []string) *Index {
	return &Index{dirs: dirs, entries: map[string]*Entry{}, now: time.Now}
}

var (
	defaultOnce  sync.Once
	defaultIndex *Index
)

// Default is the index over this user's data directories.
func Default() *Index {
	defaultOnce.Do(func() { defaultIndex = NewIndex(DataDirs()) })
	return defaultIndex
}

// rescanAfter is how stale the listing may get before a miss makes it look
// again: something installed since Atlas started should turn up without a
// restart, but a process with no desktop file should not cost a directory walk
// every second.
const rescanAfter = 30 * time.Second

// Lookup returns the entry for id, or false when there is no desktop file for it
// or the file says the application is hidden.
func (x *Index) Lookup(id string) (Entry, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if e, ok := x.entries[id]; ok {
		if e == nil {
			return Entry{}, false
		}
		return *e, true
	}
	if x.paths == nil || x.now().Sub(x.scanned) > rescanAfter {
		if _, known := x.paths[id]; !known {
			x.scan()
		}
	}
	path, ok := x.paths[id]
	if !ok {
		return Entry{}, false // not cached as a miss: it may be installed later
	}
	e, ok := parse(path, id)
	if !ok {
		x.entries[id] = nil
		return Entry{}, false
	}
	x.entries[id] = &e
	return e, true
}

// scan lists every desktop file under the data directories. A file in a
// subdirectory has an ID with the separator turned into a dash, as the
// specification says; kde4/konsole.desktop is "kde4-konsole".
func (x *Index) scan() {
	x.scanned = x.now()
	paths := map[string]string{}
	for _, d := range x.dirs {
		root := filepath.Join(d, "applications")
		_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, ".desktop") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			id := strings.TrimSuffix(strings.ReplaceAll(filepath.ToSlash(rel), "/", "-"), ".desktop")
			if _, seen := paths[id]; !seen {
				paths[id] = path
			}
			return nil
		})
	}
	x.paths = paths
}

// parse reads the [Desktop Entry] group of a desktop file.
func parse(path, id string) (Entry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, false
	}
	defer f.Close()

	e := Entry{ID: id}
	locales := localeKeys()
	best := len(locales) // rank of the Name key found so far; lower is better
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if in {
				break // past the main group; actions and the rest do not matter
			}
			in = line == "[Desktop Entry]"
			continue
		}
		if !in {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case key == "Name" && best == len(locales):
			e.Name = val
		case strings.HasPrefix(key, "Name["):
			loc := strings.TrimSuffix(strings.TrimPrefix(key, "Name["), "]")
			for rank, want := range locales {
				if loc == want && rank < best {
					e.Name, best = val, rank
				}
			}
		case key == "Icon":
			e.Icon = val
		case key == "Categories":
			for _, c := range strings.Split(val, ";") {
				if c == "TerminalEmulator" {
					e.Terminal = true
				}
			}
		case key == "Hidden" && val == "true":
			return Entry{}, false // the specification's way of deleting an entry
		}
	}
	if e.Name == "" {
		e.Name = FallbackName(id)
	}
	return e, true
}

// localeKeys are the Name[...] keys to prefer, best first, from the message
// locale: "pt_BR.UTF-8" gives pt_BR, then pt.
func localeKeys() []string {
	loc := ""
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if loc = os.Getenv(v); loc != "" {
			break
		}
	}
	if i := strings.IndexAny(loc, ".@"); i >= 0 {
		loc = loc[:i]
	}
	if loc == "" || loc == "C" || loc == "POSIX" {
		return nil
	}
	keys := []string{loc}
	if lang, _, ok := strings.Cut(loc, "_"); ok {
		keys = append(keys, lang)
	}
	return keys
}

// DataDirs are the XDG data directories, most important first, with Flatpak's
// exports added when the session did not already include them — which happens
// when Atlas is started from somewhere that never sourced Flatpak's profile
// script, and would otherwise leave every Flatpak app nameless.
func DataDirs() []string {
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	dirs := []string{}
	if dataHome != "" {
		dirs = append(dirs, dataHome)
	}
	sys := os.Getenv("XDG_DATA_DIRS")
	if sys == "" {
		sys = "/usr/local/share:/usr/share"
	}
	dirs = append(dirs, filepath.SplitList(sys)...)
	if dataHome != "" {
		dirs = append(dirs, filepath.Join(dataHome, "flatpak", "exports", "share"))
	}
	dirs = append(dirs, "/var/lib/flatpak/exports/share")

	seen := map[string]bool{}
	out := dirs[:0]
	for _, d := range dirs {
		d = filepath.Clean(d)
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}
