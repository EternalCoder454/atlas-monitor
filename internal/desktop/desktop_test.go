package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppID(t *testing.T) {
	cases := []struct {
		unit, id string
		ok       bool
	}{
		// All of these are real, from a Plasma 6 session.
		{`app-org.chromium.Chromium-662486.scope`, "org.chromium.Chromium", true},
		{`app-flatpak-com.discordapp.Discord-3262276743.scope`, "com.discordapp.Discord", true},
		{`app-com.discordapp.Discord@224f36554a1346eca11c7378b65ad44c.service`, "com.discordapp.Discord", true},
		{`app-org.kde.dolphin@765eead4667549d0ab239e9e204534a3.service`, "org.kde.dolphin", true},
		{`app-youtube\x2dmusic\x2ddesktop\x2dapp-36360.scope`, "youtube-music-desktop-app", true},
		{`app-claude\x2ddesktop\x2dunofficial@2ac530b062a14d78bdc8a36cc947f7e4.service`, "claude-desktop-unofficial", true},
		{`app-org.kde.discover.notifier@autostart.service`, "org.kde.discover.notifier", true},
		// GNOME's launcher prefix.
		{`app-gnome-org.gnome.Nautilus-4821.scope`, "org.gnome.Nautilus", true},
		{`app-gnome-firefox-9912.scope`, "firefox", true},
		// Not applications.
		{`dbus-:1.2-org.kde.kwalletd6@0.service`, "", false},
		{`pipewire.service`, "", false},
		{`plasma-kwin_wayland.service`, "", false},
		{`app.slice`, "", false},
		{`app-flatpak.slice`, "", false},
		{``, "", false},
	}
	for _, c := range cases {
		id, ok := AppID(c.unit)
		if id != c.id || ok != c.ok {
			t.Errorf("AppID(%q) = %q, %v; want %q, %v", c.unit, id, ok, c.id, c.ok)
		}
	}
}

func TestFallbackName(t *testing.T) {
	for in, want := range map[string]string{
		"com.discordapp.Discord":    "Discord",
		"org.kde.dolphin":           "dolphin",
		"firefox":                   "firefox",
		"youtube-music-desktop-app": "youtube-music-desktop-app",
		"org.gnome":                 "org.gnome", // two parts is a domain, not an app
	} {
		if got := FallbackName(in); got != want {
			t.Errorf("FallbackName(%q) = %q, want %q", in, got, want)
		}
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIndex(t *testing.T) {
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	home, sys := t.TempDir(), t.TempDir()

	write(t, filepath.Join(sys, "applications", "org.kde.konsole.desktop"), `[Desktop Entry]
Type=Application
Name=Konsole
Name[de]=Konsole (de)
Icon=utilities-terminal
Categories=Qt;KDE;System;TerminalEmulator;

[Desktop Action NewWindow]
Name=New Window
Icon=window-new
`)
	// The user's copy wins over the system's.
	write(t, filepath.Join(sys, "applications", "firefox.desktop"), "[Desktop Entry]\nName=Firefox (system)\nIcon=firefox\n")
	write(t, filepath.Join(home, "applications", "firefox.desktop"), "[Desktop Entry]\nName=Firefox\nIcon=/opt/firefox/icon.png\n")
	// A subdirectory makes a dashed ID.
	write(t, filepath.Join(sys, "applications", "kde4", "okular.desktop"), "[Desktop Entry]\nName=Okular\n")
	// Hidden=true is how an entry is deleted.
	write(t, filepath.Join(sys, "applications", "gone.desktop"), "[Desktop Entry]\nName=Gone\nHidden=true\n")
	// No Name at all.
	write(t, filepath.Join(sys, "applications", "com.example.Tool.desktop"), "[Desktop Entry]\nIcon=tool\n")

	x := NewIndex([]string{home, sys})

	e, ok := x.Lookup("org.kde.konsole")
	if !ok || e.Name != "Konsole (de)" || e.Icon != "utilities-terminal" || !e.Terminal {
		t.Errorf("konsole = %+v, %v", e, ok)
	}
	if e, _ := x.Lookup("firefox"); e.Name != "Firefox" || e.Icon != "/opt/firefox/icon.png" {
		t.Errorf("firefox = %+v; the user's own file should win", e)
	}
	if e, ok := x.Lookup("kde4-okular"); !ok || e.Name != "Okular" || e.Terminal {
		t.Errorf("kde4-okular = %+v, %v", e, ok)
	}
	if _, ok := x.Lookup("gone"); ok {
		t.Error("a Hidden=true entry was returned")
	}
	if e, ok := x.Lookup("com.example.Tool"); !ok || e.Name != "Tool" {
		t.Errorf("nameless entry = %+v, %v; want the fallback name", e, ok)
	}
	if _, ok := x.Lookup("not.installed"); ok {
		t.Error("found an entry that does not exist")
	}
}

// TestIndexNoticesInstalls checks something installed after the first lookup is
// found once the listing is old enough to be looked at again.
func TestIndexNoticesInstalls(t *testing.T) {
	dir := t.TempDir()
	x := NewIndex([]string{dir})
	now := time.Unix(1000, 0)
	x.now = func() time.Time { return now }

	if _, ok := x.Lookup("org.example.New"); ok {
		t.Fatal("found before it was installed")
	}
	write(t, filepath.Join(dir, "applications", "org.example.New.desktop"), "[Desktop Entry]\nName=New\n")
	if _, ok := x.Lookup("org.example.New"); ok {
		t.Fatal("rescanned immediately; a miss should not walk the directories every time")
	}
	now = now.Add(rescanAfter + time.Second)
	if e, ok := x.Lookup("org.example.New"); !ok || e.Name != "New" {
		t.Fatalf("not found after the listing went stale: %+v, %v", e, ok)
	}
}

func TestDataDirsIncludeFlatpak(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/home/u/.local/share")
	t.Setenv("XDG_DATA_DIRS", "/usr/share:/usr/share/")
	got := DataDirs()
	want := []string{"/home/u/.local/share", "/usr/share", "/home/u/.local/share/flatpak/exports/share", "/var/lib/flatpak/exports/share"}
	if len(got) != len(want) {
		t.Fatalf("DataDirs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DataDirs = %q, want %q", got, want)
		}
	}
}

// TestRealApplications resolves the units running on this machine, when there
// are any, and checks each resolves to a name.
func TestRealApplications(t *testing.T) {
	base := "/sys/fs/cgroup/user.slice/user-" + itoa(os.Getuid()) + ".slice/user@" + itoa(os.Getuid()) + ".service/app.slice"
	ents, err := os.ReadDir(base)
	if err != nil {
		t.Skip("no user app.slice here")
	}
	x := Default()
	n := 0
	for _, e := range ents {
		id, ok := AppID(e.Name())
		if !ok {
			continue
		}
		n++
		entry, found := x.Lookup(id)
		name := FallbackName(id)
		if found {
			name = entry.Name
		}
		t.Logf("%-70s → %-35s %q icon=%q terminal=%v", e.Name(), id, name, entry.Icon, entry.Terminal)
		if name == "" {
			t.Errorf("%s resolved to an empty name", e.Name())
		}
	}
	if n == 0 {
		t.Skip("no application units running")
	}
}

func itoa(n int) string {
	return string(appendInt(nil, n))
}

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}
