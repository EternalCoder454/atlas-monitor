package ui

import (
	"os"
	"path/filepath"
	"testing"

	"atlas-monitor/internal/desktop"
	"atlas-monitor/internal/process"
)

// testResolver resolves units against a desktop index built in a temporary
// directory, with no icon theme to consult.
func testResolver(t *testing.T) *appResolver {
	t.Helper()
	dir := t.TempDir()
	for id, body := range map[string]string{
		"org.mozilla.firefox":    "[Desktop Entry]\nName=Firefox\nIcon=org.mozilla.firefox\n",
		"com.discordapp.Discord": "[Desktop Entry]\nName=Discord\nIcon=com.discordapp.Discord\n",
		"org.kde.konsole":        "[Desktop Entry]\nName=Konsole\nIcon=utilities-terminal\nCategories=System;TerminalEmulator;\n",
	} {
		path := filepath.Join(dir, "applications", id+".desktop")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &appResolver{index: desktop.NewIndex([]string{dir}), byUnit: map[string]*appIdent{}}
}

const (
	firefoxUnit = "app-gnome-org.mozilla.firefox-4242.scope"
	discordA    = "app-flatpak-com.discordapp.Discord-3262276743.scope"
	discordB    = "app-com.discordapp.Discord@224f36554a1346eca11c7378b65ad44c.service"
)

func TestGroupByApp(t *testing.T) {
	v := &appsView{groups: make(map[groupKey]int), apps: testResolver(t)}
	procs := []process.Proc{
		{PID: 10, Name: "firefox", Unit: firefoxUnit, CPU: 5, RSS: 100, GPU: -1},
		{PID: 11, Name: "Isolated Web Co", Unit: firefoxUnit, CPU: 20, RSS: 300, GPU: -1},
		{PID: 12, Name: "WebExtensions", Unit: firefoxUnit, CPU: 1, RSS: 50, GPU: -1},
		// One application in two units — Flatpak starts a scope per launch.
		{PID: 20, Name: "Discord", Unit: discordA, CPU: 2, RSS: 200, GPU: -1},
		{PID: 21, Name: "Discord", Unit: discordB, CPU: 3, RSS: 100, GPU: -1},
		// Not an application: grouped by name, as before.
		{PID: 30, Name: "pipewire", Unit: "pipewire.service", CPU: 1, RSS: 10, GPU: -1},
		{PID: 31, Name: "pipewire", Unit: "pipewire.service", CPU: 1, RSS: 10, GPU: -1},
		// Shares an application's name without being it.
		{PID: 40, Name: "Discord", Unit: "", CPU: 0.5, RSS: 5, GPU: -1},
	}
	got := v.groupByApp(procs)
	byName := map[string]process.Proc{}
	for _, g := range got {
		if _, dup := byName[g.Name+"|"+g.Unit]; dup {
			t.Fatalf("duplicate group %q", g.Name)
		}
		byName[g.Name+"|"+g.Unit] = g
	}
	if len(got) != 4 {
		t.Fatalf("got %d groups, want 4 (Firefox, Discord, pipewire, the other Discord): %+v", len(got), got)
	}
	ff := got[0]
	if ff.Name != "Firefox" || ff.Count != 3 || ff.CPU != 26 || ff.RSS != 450 {
		t.Errorf("Firefox group = %+v; want its three processes under the desktop file's name", ff)
	}
	d := byName["Discord|"+discordA]
	if d.Count != 2 || d.CPU != 5 || d.RSS != 300 {
		t.Errorf("Discord app group = %+v; want both units' processes", d)
	}
	if other := byName["Discord|"]; other.Count != 1 || other.PID != 40 {
		t.Errorf("the unrelated process called Discord = %+v; it must keep a row of its own", other)
	}
	if pw := byName["pipewire|pipewire.service"]; pw.Count != 2 {
		t.Errorf("pipewire = %+v; want the name grouping for non-applications", pw)
	}

	// Every grouped row maps back to its own key, which is what the row
	// registry relies on.
	for _, g := range got {
		g := g
		members := 0
		v.lastSnap = procs
		for _, m := range v.membersOf(&g) {
			members++
			if v.keyOf(&m) != v.keyOf(&g) {
				t.Errorf("member %d of %q has a different key", m.PID, g.Name)
			}
		}
		if members != g.Count {
			t.Errorf("%q: membersOf found %d, Count is %d", g.Name, members, g.Count)
		}
	}
}

func TestNameAndPIDColumnsForGroups(t *testing.T) {
	one := process.Proc{PID: 7, Name: "bash", Count: 1}
	many := process.Proc{PID: 7, Name: "Firefox", Count: 12}
	if got := string(appendName(nil, &one)); got != "bash" {
		t.Errorf("name of a single process = %q", got)
	}
	if got := string(appendName(nil, &many)); got != "Firefox (12)" {
		t.Errorf("name of a group = %q", got)
	}
	if got := string(appendPID(nil, &one)); got != "7" {
		t.Errorf("PID of a single process = %q", got)
	}
	if got := string(appendPID(nil, &many)); got != "—" {
		t.Errorf("PID of a group = %q; a group has no one PID", got)
	}
}

// TestSearchFindsApplicationProcesses: searching for an application finds its
// helpers even when they are named something else entirely.
func TestSearchFindsApplicationProcesses(t *testing.T) {
	v := &appsView{apps: testResolver(t)}
	v.search = lowerASCII("firefox")
	helper := &procRow{live: true, proc: process.Proc{PID: 11, Name: "Isolated Web Co", Unit: firefoxUnit}}
	stranger := &procRow{live: true, proc: process.Proc{PID: 99, Name: "Isolated Web Co", Unit: "other.service"}}
	if !v.matchesRow(helper) {
		t.Error("a Firefox process was not found by searching for Firefox")
	}
	if v.matchesRow(stranger) {
		t.Error("a process outside Firefox matched a search for it")
	}
}

func TestResolverKnowsTerminals(t *testing.T) {
	r := testResolver(t)
	if a := r.of("app-org.kde.konsole@abc.service"); a == nil || !a.terminal || a.name != "Konsole" {
		t.Errorf("konsole = %+v", a)
	}
	if a := r.of("pipewire.service"); a != nil {
		t.Errorf("a system service resolved to an application: %+v", a)
	}
	// Twice, to go through the cache.
	if a, b := r.of(discordA), r.of(discordA); a == nil || a != b || a.icon != "com.discordapp.Discord" {
		t.Errorf("discord = %+v then %+v", a, b)
	}
}
