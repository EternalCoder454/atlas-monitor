package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"atlas-monitor/internal/config"
)

// serveChannel stands in for raw.githubusercontent.com: it serves VERSION and
// CHANGELOG.md for one branch and points the checker at itself for the duration
// of the test.
//
// The version check is the only thing between a packaged install and never
// hearing about a release, so it is tested for real rather than mocked at the
// function boundary — but against a local server, so the suite does not depend on
// the network or on whatever happens to be on the branch today.
func serveChannel(t *testing.T, branch, version, changelog string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + branch + "/VERSION":
			w.Write([]byte(version + "\n"))
		case "/" + branch + "/CHANGELOG.md":
			if changelog == "" {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(changelog))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	old := rawBase
	rawBase = srv.URL
	t.Cleanup(func() { rawBase = old })
	return srv
}

// TestVersionsCompareAsNumbers is the bug this would otherwise have: compared as
// strings, "0.9.0" sorts after "0.11.0", so every install past 0.9 would have sat
// there insisting it was current. A packaged install has nothing but these two
// numbers to go on, so getting the order right is the whole check.
func TestVersionsCompareAsNumbers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.11.0", "0.9.0", 1}, // the one that string comparison gets backwards
		{"0.9.0", "0.11.0", -1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"0.11.0", "0.11", 1},    // a missing component is older
		{"v1.2.0", "1.2.0", 0},   // a leading v is decoration
		{" 1.2.0\n", "1.2.0", 0}, // and so is whitespace
		{"10.0.0", "9.0.0", 1},   // two digits against one
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestNoDowngradeIsOffered: a build ahead of the branch — a beta user who
// switched to Release, or someone running a local build — must not be told to
// "update" to something older.
func TestNoDowngradeIsOffered(t *testing.T) {
	serveChannel(t, config.DefaultChannel, "1.0.0", "")

	a := App{version: "1.1.0"}
	info, err := a.checkRemote(config.DefaultChannel)
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Errorf("1.1.0 was offered a downgrade to 1.0.0: %q", info.Summary)
	}
	if !strings.Contains(info.Summary, "Up to date") {
		t.Errorf("Summary = %q, want it to say up to date", info.Summary)
	}
}

// sameBuild is a channel that carries this same build — the full application
// or the minimal one — so checking it is an update and not a switch.
func sameBuild() (channel, name string) {
	if config.Minimal {
		return config.ChannelMinimal, "Minimal"
	}
	return config.ChannelBeta, "Beta"
}

// otherBuild is a channel that carries the other build.
func otherBuild() (channel, name string) {
	if config.Minimal {
		return config.ChannelRelease, "Release"
	}
	return config.ChannelMinimal, "Minimal"
}

// TestSwitchingBuildsIsOffered: moving between the full application and the
// minimal one is a different build, so it is offered at the same version and
// says so — except to a copy a package manager owns, which cannot act on it and
// would be told the same thing on every check.
func TestSwitchingBuildsIsOffered(t *testing.T) {
	channel, name := otherBuild()
	serveChannel(t, channel, "1.0.0", "")

	a := &App{version: "1.0.0"}
	a.pinInstall(Install{Kind: Standalone, Binary: "/opt/atlas/bin/atlas-monitor"})
	info, err := a.checkRemote(channel)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || !info.Switch {
		t.Fatalf("Available = %v, Switch = %v: the other build was not offered (%q)", info.Available, info.Switch, info.Summary)
	}
	for _, want := range []string{"Switch", name, "1.0.0"} {
		if !strings.Contains(info.Summary, want) {
			t.Errorf("Summary = %q, want it to mention %q", info.Summary, want)
		}
	}

	pkg := &App{version: "1.0.0"}
	pkg.pinInstall(Install{Kind: FromPackage, Binary: "/usr/bin/atlas-monitor", Manager: "pacman", Package: "atlas-monitor"})
	if info, err := pkg.checkRemote(channel); err != nil || info.Available {
		t.Errorf("a packaged copy was offered %q (err %v), which its package manager cannot do", info.Summary, err)
	}
}

// TestRemoteCheckNamesTheChannel: the summary is the whole message in Settings,
// so it has to say which channel it looked at and both versions.
func TestRemoteCheckNamesTheChannel(t *testing.T) {
	channel, name := sameBuild()
	serveChannel(t, channel, "0.12.0", "# What's new\n\n## 0.12.0\n\n- New things\n")

	a := App{version: "0.11.0"}
	info, err := a.checkRemote(channel)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available {
		t.Fatal("0.11.0 was not offered 0.12.0")
	}
	for _, want := range []string{name, "0.11.0", "0.12.0"} {
		if !strings.Contains(info.Summary, want) {
			t.Errorf("Summary = %q, want it to mention %q", info.Summary, want)
		}
	}
	if len(info.Changes) != 1 || info.Changes[0] != "New things" {
		t.Errorf("Changes = %q, want the one bullet for 0.12.0", info.Changes)
	}
}

// TestMissingChangelogStillOffersTheUpdate: the changelog is a courtesy. A
// branch without one, or a fetch that fails for it alone, must not turn an
// available update into an error.
func TestMissingChangelogStillOffersTheUpdate(t *testing.T) {
	serveChannel(t, config.DefaultChannel, "3.0.0", "") // 404s the changelog

	a := App{version: "2.0.0"}
	info, err := a.checkRemote(config.DefaultChannel)
	if err != nil {
		t.Fatalf("a missing changelog broke the check: %v", err)
	}
	if !info.Available || info.Version != "3.0.0" {
		t.Errorf("got %+v, want 3.0.0 available", info)
	}
}

// TestUnreachableChannelIsAnOrdinaryError: offline is not a crash and not a
// silent "up to date", which would be a lie.
func TestUnreachableChannelIsAnOrdinaryError(t *testing.T) {
	old := rawBase
	// A port nothing is listening on, rather than a name that would depend on DNS.
	rawBase = "http://127.0.0.1:1"
	t.Cleanup(func() { rawBase = old })

	a := App{version: "1.0.0"}
	info, err := a.checkRemote(config.DefaultChannel)
	if err == nil {
		t.Fatal("an unreachable channel reported success")
	}
	if info.Available {
		t.Error("an update was offered by a check that failed")
	}
	if strings.Contains(err.Error(), "make install") {
		t.Errorf("err = %q, which tells the user to run make install", err)
	}
}

// TestEmptyVersionIsNotTreatedAsAVersion: a proxy or a captive portal answering
// 200 with nothing must not be read as "the channel is on version ”".
func TestEmptyVersionIsNotTreatedAsAVersion(t *testing.T) {
	serveChannel(t, config.DefaultChannel, "", "")

	a := App{version: "1.0.0"}
	if _, err := a.checkRemote(config.DefaultChannel); err == nil {
		t.Error("an empty VERSION was accepted")
	}
}
