package app

import (
	"os"
	"path/filepath"
)

// Making a shipped Atlas find its own GTK data on Windows.
//
// On Linux, GTK's icons, its settings schemas and gdk-pixbuf's image loaders are
// installed in fixed places and everything finds them. A Windows build has no
// fixed places: it is a folder someone unzipped, and the paths compiled into the
// GTK DLLs point at wherever MSYS2 happened to be on the machine that built them.
// Left alone, GTK aborts at startup because it cannot find its schemas, and every
// icon comes up as an empty square because the SVG loader is not registered.
//
// So the three locations are pointed at the folder the exe is actually in. This
// runs in init, before anything in this package touches GLib — once GTK has
// started, reading these again is not something it does.
//
// Nothing here overrides a variable that is already set. A developer running the
// app from an MSYS2 shell has a working GTK installation on the system path and
// should keep it; so should anyone debugging with their own schema directory.
func init() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	root := filepath.Dir(exe)

	// Each of these is only set when the directory is actually there, so a build
	// run from a source tree — where none of it has been staged — is left to find
	// GTK the ordinary way rather than being pointed at directories that do not
	// exist.
	setIfDirExists("XDG_DATA_DIRS", filepath.Join(root, "share"))
	setIfDirExists("GSETTINGS_SCHEMA_DIR", filepath.Join(root, "share", "glib-2.0", "schemas"))
	setIfFileExists("GDK_PIXBUF_MODULE_FILE",
		filepath.Join(root, "lib", "gdk-pixbuf-2.0", "2.10.0", "loaders.cache"))
}

func setIfDirExists(key, dir string) {
	if _, set := os.LookupEnv(key); set {
		return
	}
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		os.Setenv(key, dir)
	}
}

func setIfFileExists(key, path string) {
	if _, set := os.LookupEnv(key); set {
		return
	}
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		os.Setenv(key, path)
	}
}
