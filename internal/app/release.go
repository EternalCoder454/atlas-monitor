package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Finding out whether a newer version exists, without a source checkout.
//
// The original check was a `git fetch` in the checkout that `make install`
// recorded, which is exact — it compares commits — but it means a copy installed
// by pacman, or unpacked from a tarball, could not even find out that a new
// version had been released. It got an error where the answer should have been,
// which is how "it doesn't update" starts.
//
// Two files answer the question for everyone else: VERSION and CHANGELOG.md, as
// they stand on the channel branch. They are fetched over plain HTTPS with no
// API, no token and no rate limit worth worrying about, and nothing is written to
// disk. What it cannot do is notice commits that have not changed VERSION, so a
// checkout still uses git — see CheckUpdate.

const (
	repoURL = "https://github.com/EternalCoder454/atlas-monitor"
	rawURL  = "https://raw.githubusercontent.com/EternalCoder454/atlas-monitor"
)

// rawBase is where the version check reads VERSION and CHANGELOG.md from. It is
// a variable so that the tests can point it at a local server: the check is the
// only thing standing between a packaged install and never hearing about an
// update, and testing it against the real GitHub would make the suite depend on
// the network and on what happens to be on the branch that day.
var rawBase = rawURL

// fetchTimeout bounds the whole check. It is a button press with a spinner on
// it, so a network that is not answering has to become an error reasonably soon
// rather than leaving the dialog spinning.
const fetchTimeout = 12 * time.Second

// maxFetchBytes caps each file read. VERSION is a few bytes and the changelog a
// few tens of kilobytes; anything far past that is not what we asked for, and a
// redirect to something enormous should not be read into memory.
const maxFetchBytes = 512 << 10

// checkRemote reports what the channel branch is offering, by version.
//
// It is used when there is no checkout to compare commits in. "Newer" is a
// version comparison rather than a string inequality, so a local build that is
// somehow ahead of the branch is not offered a downgrade.
func (a *App) checkRemote(channel string) (UpdateInfo, error) {
	var info UpdateInfo

	version, err := fetchText(rawBase + "/" + channel + "/VERSION")
	if err != nil {
		return info, fmt.Errorf("couldn't reach GitHub")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return info, fmt.Errorf("couldn't read the latest version")
	}

	name := channelName(channel)
	local := strings.TrimSpace(a.version)

	if compareVersions(version, local) <= 0 {
		info.Summary = fmt.Sprintf("Up to date on %s (v%s)", name, local)
		return info, nil
	}

	info.Available = true
	info.Version = version
	info.Summary = fmt.Sprintf("%s update available: v%s → v%s", name, local, version)
	// The changelog is a courtesy: the update is worth offering without it.
	if md, err := fetchText(rawBase + "/" + channel + "/CHANGELOG.md"); err == nil {
		info.Changes = ChangelogFor(md, version)
	}
	return info, nil
}

// channelName is the word for a branch that a person would recognise. This build
// has one channel and one only — see config.MinimalChannel — so there is nothing
// to choose between: whatever it is asked about, the answer is Minimal.
func channelName(string) string { return "Minimal" }

// fetchText GETs a small text file.
func fetchText(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "atlas-monitor")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// compareVersions orders two dotted versions: -1 if a is older than b, 0 if they
// are the same, 1 if a is newer.
//
// Numeric per component, because that is the whole point — as strings "0.9.0" is
// after "0.11.0" and the app would sit on an old version insisting it was
// current. A component that is not a number, and anything trailing like "-rc1",
// is compared as text so that two of them still order consistently.
func compareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := at(as, i), at(bs, i)
		nx, errX := strconv.Atoi(x)
		ny, errY := strconv.Atoi(y)
		switch {
		case errX == nil && errY == nil:
			if nx != ny {
				return sign(nx - ny)
			}
		case x != y:
			// A missing component is older than a present one: 0.11 precedes
			// 0.11.1, and "0.11.0" precedes "0.11.0-rc1" only in that the latter
			// sorts later as text, which is consistent if not meaningful.
			return strings.Compare(x, y)
		}
	}
	return 0
}

func splitVersion(v string) []string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return nil
	}
	return strings.Split(v, ".")
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
