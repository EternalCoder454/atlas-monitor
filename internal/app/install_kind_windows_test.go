package app

import (
	"strings"
	"testing"

	"atlas-monitor/internal/config"
)

// TestWindowsNeverOffersToInstallOverItself.
//
// The Linux update path pulls the source and runs make. A Windows machine that
// downloaded a packaged exe has no toolchain, and could not replace the running
// file even with one — so SelfUpdatable has to be false for every kind of install,
// not only the ones a package manager owns.
func TestWindowsNeverOffersToInstallOverItself(t *testing.T) {
	for _, in := range []Install{
		{Kind: FromSource, Source: `C:\src\atlas-monitor`},
		{Kind: Standalone, Binary: `C:\Users\someone\Atlas\atlas-monitor.exe`},
		{Kind: FromPackage, Manager: "pacman", Binary: `C:\msys64\usr\bin\atlas-monitor.exe`},
	} {
		if in.SelfUpdatable() {
			t.Errorf("kind %d offered to install over itself on Windows", in.Kind)
		}
	}
	if canSelfInstall {
		t.Error("canSelfInstall is true on Windows")
	}
}

// TestWindowsWordingExplainsTheRealReason: the Linux text talks about package
// managers and write permission, neither of which is why this cannot update here.
func TestWindowsWordingExplainsTheRealReason(t *testing.T) {
	heading, body := managedWording(Install{
		Kind: Standalone, Binary: `C:\Program Files\Atlas\atlas-monitor.exe`,
	})
	if heading == "" || body == "" {
		t.Fatal("no wording at all")
	}
	// It has to say the file is locked, which is the actual obstacle.
	if !strings.Contains(strings.ToLower(body), "locked") {
		t.Errorf("body = %q, want it to say the file is locked while running", body)
	}
	for _, wrong := range []string{"package manager", "pacman", "make install", "administrator rights"} {
		if strings.Contains(strings.ToLower(heading+body), wrong) {
			t.Errorf("the wording mentions %q, which is not why it cannot update here", wrong)
		}
	}
}

// TestDownloadPageIsAReleaseURL: it is what the dialog's button opens, so a wrong
// one sends the user somewhere unhelpful.
//
// Minimal's is not /releases/latest: GitHub resolves that to the newest release
// of either build, which is usually the full application.
func TestDownloadPageIsAReleaseURL(t *testing.T) {
	for _, channel := range config.Channels {
		url := downloadPage(channel)
		if !strings.HasPrefix(url, "https://") || !strings.Contains(url, "/releases") {
			t.Errorf("downloadPage(%q) = %q, want an https releases URL", channel, url)
		}
		minimal := channel == config.ChannelMinimal
		if minimal == strings.HasSuffix(url, "/releases/latest") || minimal != strings.Contains(url, "minimal") {
			t.Errorf("downloadPage(%q) = %q, want the releases of that channel's build", channel, url)
		}
	}
	// And the minimal ones: /releases/latest is usually the full application.
	if strings.HasSuffix(url, "/releases/latest") || !strings.Contains(url, "minimal") {
		t.Errorf("downloadPage() = %q, want minimal's releases, not the latest of either build", url)
	}
}
