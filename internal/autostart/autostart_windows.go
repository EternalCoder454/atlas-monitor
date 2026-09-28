package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// What starts at login on Windows.
//
// There are four places to look, and Atlas reads all of them because Task
// Manager's Startup tab does:
//
//   - HKCU\...\CurrentVersion\Run — this user's programs
//   - HKLM\...\CurrentVersion\Run — every user's
//   - the user's Startup folder, which holds shortcuts
//   - the shared Startup folder
//
// Switching one off is the interesting part. Deleting the registry value would
// work and would also throw away the command, so there would be no way back.
// Windows itself does not do that: it writes a value under StartupApproved, whose
// first byte says whether the entry is enabled, and leaves the Run value alone.
// That is what this does, which means an entry disabled here shows as disabled in
// Task Manager and the other way round.
//
// Machine-wide *registry* entries are listed but cannot be changed: their approval
// lives in the machine hive, which needs administrator rights to write, and Atlas
// does not run elevated — the same position it takes on services. Entries in the
// shared Startup folder are a different case, because their approval is per-user
// even though the shortcut is not. See SetEnabled.

// Registry locations. The approval subkeys sit beside the Run keys.
const (
	runKey         = `Software\Microsoft\Windows\CurrentVersion\Run`
	runApprovedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
	folderApproved = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder`
)

// approvalEnabled and approvalDisabled are the first byte of a StartupApproved
// value. The rest of it is a timestamp Windows writes and nothing reads back, so
// it is left as zeroes.
const (
	approvalEnabled  = 0x02
	approvalDisabled = 0x03
	approvalLen      = 12
)

// errNeedsAdmin is returned for an entry whose approval lives in the machine hive.
var errNeedsAdmin = errors.New("this program is registered for every user, and " +
	"changing that needs administrator rights — use Task Manager's Startup tab " +
	"from an administrator account")

// Reader reads the login entries. It holds no state; the locations are fixed.
type Reader struct{}

// New returns a reader for the standard locations.
func New() *Reader { return &Reader{} }

// List returns every entry, sorted by the name people read.
func (r *Reader) List() []Entry {
	var out []Entry

	// The registry Run keys.
	for _, src := range []struct {
		hive   registry.Key
		system bool
	}{
		{registry.CURRENT_USER, false},
		{registry.LOCAL_MACHINE, true},
	} {
		approved := readApprovals(src.hive, runApprovedKey)
		for _, e := range runEntries(src.hive, src.system) {
			e.Disabled = approved[strings.ToLower(e.Name)]
			out = append(out, e)
		}
	}

	// The Startup folders, whose entries are files rather than registry values.
	userApproved := readApprovals(registry.CURRENT_USER, folderApproved)
	for _, src := range []struct {
		dir    string
		system bool
	}{
		{startupDir(false), false},
		{startupDir(true), true},
	} {
		for _, e := range folderEntries(src.dir, src.system) {
			e.Disabled = userApproved[strings.ToLower(e.Name)]
			out = append(out, e)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// runEntries reads one Run key.
func runEntries(hive registry.Key, system bool) []Entry {
	k, err := registry.OpenKey(hive, runKey, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()

	names, err := k.ReadValueNames(0)
	if err != nil {
		return nil
	}

	var out []Entry
	for _, name := range names {
		// A Run value is normally a string; anything else is not a command and
		// GetStringValue declines it, which is the filter we want.
		cmd, _, err := k.GetStringValue(name)
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name:        name,
			Exec:        strings.TrimSpace(cmd),
			File:        hiveName(hive) + `\` + runKey + ` → ` + name,
			System:      system,
			RunsHere:    true,
			hasName:     true,
			approvalKey: runApprovedKey,
		})
	}
	return out
}

// folderEntries reads a Startup folder.
func folderEntries(dir string, system bool) []Entry {
	if dir == "" {
		return nil
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []Entry
	for _, it := range items {
		if it.IsDir() {
			continue
		}
		name := it.Name()
		if strings.EqualFold(name, "desktop.ini") {
			continue // the folder's own settings, not a program
		}
		path := filepath.Join(dir, name)
		out = append(out, Entry{
			// The shortcut's file name is what Task Manager shows, extension and
			// all, and it is what the approval value is keyed by — so it is kept
			// as it stands rather than tidied.
			Name:        name,
			Exec:        path,
			File:        path,
			System:      system,
			RunsHere:    true,
			hasName:     true,
			approvalKey: folderApproved,
		})
	}
	return out
}

// readApprovals returns the entries a StartupApproved key says are switched off,
// keyed by lower-cased name.
func readApprovals(hive registry.Key, sub string) map[string]bool {
	out := map[string]bool{}
	k, err := registry.OpenKey(hive, sub, registry.QUERY_VALUE)
	if err != nil {
		return out
	}
	defer k.Close()

	names, err := k.ReadValueNames(0)
	if err != nil {
		return out
	}
	for _, name := range names {
		v, _, err := k.GetBinaryValue(name)
		if err != nil {
			continue
		}
		out[strings.ToLower(name)] = isDisabled(v)
	}
	return out
}

// isDisabled reads a StartupApproved value.
//
// Only the low bit of the first byte matters. Windows writes 0x02 for enabled and
// 0x03 for disabled, but values with the higher bits set have been seen in the
// wild — 0x06 among them — so this tests the bit rather than comparing the byte.
// Getting it backwards would present every enabled program as switched off, which
// is the kind of mistake that looks like a working feature.
func isDisabled(v []byte) bool {
	if len(v) == 0 {
		return false // nothing recorded means nothing has switched it off
	}
	return v[0]&0x01 != 0
}

// startupDir is the Startup folder for this user, or the shared one.
//
// The paths are derived from the environment rather than asked for through
// SHGetKnownFolderPath: the two variables involved are set on every Windows
// install, and the alternative is a COM call for a string.
func startupDir(system bool) string {
	base := os.Getenv("APPDATA")
	if system {
		base = os.Getenv("PROGRAMDATA")
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

// hiveName is how the hive is written in a path, for the entry's File field.
func hiveName(hive registry.Key) string {
	if hive == registry.LOCAL_MACHINE {
		return "HKEY_LOCAL_MACHINE"
	}
	return "HKEY_CURRENT_USER"
}

// SetEnabled switches an entry on or off.
//
// The Run value is never touched: switching off writes the StartupApproved value
// that Windows itself uses, so the command survives and Task Manager agrees about
// the state.
//
// What can be changed depends on where the *approval* lives, not on where the entry
// lives — which is not the same thing. A machine-wide Run entry is approved under
// HKEY_LOCAL_MACHINE and needs administrator rights. A shortcut in the shared
// Startup folder is approved under HKEY_CURRENT_USER like any other, so this user
// can switch it off for themselves, exactly as Task Manager lets them. Refusing
// both would have been safe and would also have told the user something untrue.
func SetEnabled(e Entry, on bool) error {
	if e.approvalKey == "" {
		return errors.New("this entry has no approval setting to change")
	}
	if e.System && e.approvalKey == runApprovedKey {
		return errNeedsAdmin
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, e.approvalKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	v := make([]byte, approvalLen)
	if on {
		v[0] = approvalEnabled
	} else {
		v[0] = approvalDisabled
	}
	return k.SetBinaryValue(e.Name, v)
}
