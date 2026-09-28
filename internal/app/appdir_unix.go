//go:build unix

package app

// localAppData has no meaning on Unix, so the XDG default under the home
// directory is used instead. See atlasDir.
func localAppData() string { return "" }
