package ease

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeUnits hands out cumulative CPU time that the test drives by setting how
// busy each unit is.
type fakeUnits struct {
	usage map[string]uint64
	load  map[string]float64 // percent of one core
	w     *fakeWeights       // what the kernel would be applying
}

func (f *fakeUnits) advance(d time.Duration) {
	for u, pct := range f.load {
		f.usage[u] += uint64(pct / 100 * float64(d.Microseconds()))
	}
}

func (f *fakeUnits) Sample() (map[string]Sample, error) {
	out := map[string]Sample{}
	for u, v := range f.usage {
		w := uint64(DefaultWeight)
		if set, ok := f.w.w[u]; ok && set != Unset {
			w = set
		}
		out[u] = Sample{Usage: v, Weight: w}
	}
	return out, nil
}

type fakeWeights struct {
	w      map[string]uint64
	refuse map[string]bool
}

func (f *fakeWeights) Weight(unit string) (uint64, error) {
	if w, ok := f.w[unit]; ok {
		return w, nil
	}
	return Unset, nil
}

func (f *fakeWeights) SetWeight(unit string, w uint64) error {
	if f.refuse[unit] {
		return errors.New("refused")
	}
	f.w[unit] = w
	return nil
}

type fakeAudio struct {
	playing map[string]bool
	err     error
	calls   int
}

func (f *fakeAudio) AudibleApps() (map[string]bool, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]bool{}
	for k, v := range f.playing {
		out[k] = v
	}
	return out, nil
}

// ident treats app-<id>-<n>.scope as an application, and "term" as a terminal.
func ident(unit string) (string, string, bool, bool) {
	rest, ok := strings.CutPrefix(unit, "app-")
	if !ok {
		return "", "", false, false
	}
	id := strings.SplitN(strings.TrimSuffix(rest, ".scope"), "-", 2)[0]
	return id, strings.ToUpper(id[:1]) + id[1:], id == "term", true
}

type rig struct {
	t     *testing.T
	units *fakeUnits
	w     *fakeWeights
	audio *fakeAudio
	c     *Controller
	now   time.Time
	saved map[string]uint64
}

func newRig(t *testing.T, units ...string) *rig {
	r := &rig{
		t:     t,
		units: &fakeUnits{usage: map[string]uint64{}, load: map[string]float64{}},
		w:     &fakeWeights{w: map[string]uint64{}, refuse: map[string]bool{}},
		audio: &fakeAudio{playing: map[string]bool{}},
		now:   time.Unix(1_000_000, 0),
	}
	r.units.w = r.w
	for _, u := range units {
		r.units.usage[u] = 0
	}
	r.c = New(r.units, r.w, r.audio, ident, "atlas")
	r.c.now = func() time.Time { return r.now }
	r.c.OnChange(func(m map[string]uint64) { r.saved = m })
	r.c.SetAutomatic(true)
	return r
}

// run ticks every five seconds for d.
func (r *rig) run(d time.Duration) {
	for end := r.now.Add(d); r.now.Before(end); {
		r.now = r.now.Add(5 * time.Second)
		r.units.advance(5 * time.Second)
		r.c.Tick()
	}
}

func (r *rig) status(id string) Status {
	for _, a := range r.c.Snapshot() {
		if a.ID == id {
			return a.Status
		}
	}
	return Normal
}

func TestBusyAppIsEasedAndPutBack(t *testing.T) {
	r := newRig(t, "app-game-1.scope", "app-editor-2.scope")
	r.units.load["app-game-1.scope"] = 90
	r.units.load["app-editor-2.scope"] = 3

	r.run(25 * time.Second)
	if _, touched := r.w.w["app-game-1.scope"]; touched {
		t.Fatal("eased before being busy for half a minute")
	}
	if r.status("game") != Busy {
		t.Errorf("status while counting = %v, want Busy", r.status("game"))
	}
	r.run(15 * time.Second)
	if r.w.w["app-game-1.scope"] != EasedWeight {
		t.Fatalf("weight after 40 s at 90%% = %d, want %d", r.w.w["app-game-1.scope"], EasedWeight)
	}
	if _, touched := r.w.w["app-editor-2.scope"]; touched {
		t.Error("a quiet application was touched")
	}
	if r.status("game") != EasedAuto || r.saved["app-game-1.scope"] != Unset {
		t.Errorf("status %v, saved %v", r.status("game"), r.saved)
	}

	// Calm, but not for long enough.
	r.units.load["app-game-1.scope"] = 2
	r.run(40 * time.Second)
	if r.w.w["app-game-1.scope"] != EasedWeight {
		t.Fatal("put back after 40 s calm; it should wait a minute so it does not flap")
	}
	r.run(30 * time.Second)
	if r.w.w["app-game-1.scope"] != Unset {
		t.Fatalf("weight after calming = %d, want it unset again, as it was", r.w.w["app-game-1.scope"])
	}
	if len(r.saved) != 0 {
		t.Errorf("crash-recovery state still lists %v", r.saved)
	}
}

func TestSoundIsLeftAlone(t *testing.T) {
	r := newRig(t, "app-music-1.scope")
	r.units.load["app-music-1.scope"] = 80
	r.audio.playing["music"] = true
	r.run(2 * time.Minute)
	if _, touched := r.w.w["app-music-1.scope"]; touched {
		t.Fatal("an application playing sound was eased")
	}
	if r.status("music") != KeptSound {
		t.Errorf("status = %v, want KeptSound", r.status("music"))
	}

	// Stops playing: eased on the next check.
	r.audio.playing["music"] = false
	r.run(10 * time.Second)
	if r.w.w["app-music-1.scope"] != EasedWeight {
		t.Fatal("not eased once the sound stopped")
	}
	// Starts again: put back within one audio check, however busy it is.
	r.audio.playing["music"] = true
	r.run(audioEvery + 5*time.Second)
	if w := r.w.w["app-music-1.scope"]; w != Unset {
		t.Fatalf("weight after sound started = %d; want it put back", w)
	}
}

func TestNoAudioAnswerMeansNoEasing(t *testing.T) {
	r := newRig(t, "app-game-1.scope")
	r.units.load["app-game-1.scope"] = 90
	r.audio.err = errors.New("pw-dump failed")
	r.run(2 * time.Minute)
	if _, touched := r.w.w["app-game-1.scope"]; touched {
		t.Fatal("eased without knowing whether it was playing")
	}
}

func TestTerminalsSelfAndNeverAreKept(t *testing.T) {
	r := newRig(t, "app-term-1.scope", "app-atlas-2.scope", "app-build-3.scope")
	for u := range r.units.usage {
		r.units.load[u] = 95
	}
	r.c.SetNever([]string{"build"})
	r.run(2 * time.Minute)
	if len(r.w.w) != 0 {
		t.Fatalf("weights set: %v; a terminal, Atlas itself and a never-listed app must all be left alone", r.w.w)
	}
	if r.status("term") != KeptTerminal || r.status("build") != KeptNever {
		t.Errorf("statuses: term %v, build %v", r.status("term"), r.status("build"))
	}

	// Taking an app off the never list lets it be eased; putting it back
	// restores it at once.
	r.c.SetNever(nil)
	r.run(10 * time.Second)
	if r.w.w["app-build-3.scope"] != EasedWeight {
		t.Fatal("not eased once off the never list")
	}
	r.c.SetNever([]string{"build"})
	if r.w.w["app-build-3.scope"] != Unset {
		t.Fatal("not put back as soon as it was put on the never list")
	}
}

func TestManualEaseIsTheUsersToUndo(t *testing.T) {
	r := newRig(t, "app-sync-1.scope", "app-game-2.scope")
	r.units.load["app-game-2.scope"] = 90
	r.run(10 * time.Second)
	if err := r.c.Ease("sync"); err != nil {
		t.Fatal(err)
	}
	if r.w.w["app-sync-1.scope"] != EasedWeight || r.status("sync") != EasedManual {
		t.Fatal("manual ease did not take")
	}
	if _, listed := r.saved["app-sync-1.scope"]; listed {
		t.Error("a manual ease was put in crash recovery, which would undo it after a restart")
	}
	// Quiet and even playing sound: a manual ease stays.
	r.audio.playing["sync"] = true
	r.run(3 * time.Minute)
	if r.w.w["app-sync-1.scope"] != EasedWeight {
		t.Fatal("a manual ease was undone automatically")
	}
	// Turning automatic off keeps manual eases and undoes automatic ones.
	if r.w.w["app-game-2.scope"] != EasedWeight {
		t.Fatal("game was not eased automatically")
	}
	r.c.SetAutomatic(false)
	if r.w.w["app-game-2.scope"] != Unset || r.w.w["app-sync-1.scope"] != EasedWeight {
		t.Fatalf("after turning automatic off: %v", r.w.w)
	}
	if err := r.c.Restore("sync"); err != nil || r.w.w["app-sync-1.scope"] != Unset {
		t.Fatalf("Restore: %v, weight %d", err, r.w.w["app-sync-1.scope"])
	}
}

func TestNewWindowOfAnEasedAppIsEased(t *testing.T) {
	r := newRig(t, "app-browser-1.scope")
	r.units.load["app-browser-1.scope"] = 90
	r.run(40 * time.Second)
	r.units.usage["app-browser-2.scope"] = 0
	r.run(5 * time.Second)
	if r.w.w["app-browser-2.scope"] != EasedWeight {
		t.Fatal("a new unit of an eased application was not eased with it")
	}
	// And a closed one is simply forgotten.
	delete(r.units.usage, "app-browser-1.scope")
	delete(r.units.load, "app-browser-1.scope")
	r.run(5 * time.Second)
	if _, listed := r.saved["app-browser-1.scope"]; listed {
		t.Error("a unit that ended is still in crash recovery")
	}
}

func TestShutdownPutsBackAutomaticOnly(t *testing.T) {
	r := newRig(t, "app-game-1.scope", "app-sync-2.scope")
	r.units.load["app-game-1.scope"] = 90
	r.run(10 * time.Second)
	r.c.Ease("sync")
	r.run(40 * time.Second)
	r.c.Shutdown()
	if r.w.w["app-game-1.scope"] != Unset || r.w.w["app-sync-2.scope"] != EasedWeight {
		t.Fatalf("after shutdown: %v", r.w.w)
	}
}

func TestPartialRefusal(t *testing.T) {
	r := newRig(t, "app-game-1.scope", "app-game-2.scope")
	r.w.refuse["app-game-2.scope"] = true
	r.units.load["app-game-1.scope"] = 60
	r.units.load["app-game-2.scope"] = 30
	r.run(40 * time.Second)
	if r.status("game") != EasedAuto || r.w.w["app-game-1.scope"] != EasedWeight {
		t.Fatalf("status %v, weights %v", r.status("game"), r.w.w)
	}
	r.units.load["app-game-1.scope"], r.units.load["app-game-2.scope"] = 1, 1
	r.run(70 * time.Second)
	if r.status("game") == EasedAuto || r.w.w["app-game-1.scope"] != Unset {
		t.Fatalf("not put back: status %v, weights %v", r.status("game"), r.w.w)
	}
}

func TestAutomaticOffDoesNothing(t *testing.T) {
	r := newRig(t, "app-game-1.scope")
	r.c.SetAutomatic(false)
	r.units.load["app-game-1.scope"] = 99
	r.run(5 * time.Minute)
	if len(r.w.w) != 0 || r.audio.calls != 0 {
		t.Fatalf("with automatic off: weights %v, %d audio checks", r.w.w, r.audio.calls)
	}
	if r.status("game") != Busy {
		t.Errorf("status = %v; the page should still show it as busy", r.status("game"))
	}
}

// TestFocusedAppIsLeftAlone covers uresourced, which Fedora runs: it raises the
// focused application to 300 and puts it back when focus moves on. That makes
// the raised application the one being used, which is never eased, and means a
// weight Atlas set can be replaced from outside at any moment.
func TestFocusedAppIsLeftAlone(t *testing.T) {
	r := newRig(t, "app-game-1.scope")
	r.units.load["app-game-1.scope"] = 90
	r.w.w["app-game-1.scope"] = 300 // focused
	r.run(2 * time.Minute)
	if r.w.w["app-game-1.scope"] != 300 {
		t.Fatalf("the focused application's weight became %d", r.w.w["app-game-1.scope"])
	}
	if r.status("game") != KeptInUse {
		t.Errorf("status = %v, want KeptInUse", r.status("game"))
	}

	// Focus moves on; uresourced puts it back to the default. Busy for half a
	// minute more, it is eased.
	delete(r.w.w, "app-game-1.scope")
	r.run(35 * time.Second)
	if r.w.w["app-game-1.scope"] != EasedWeight {
		t.Fatal("not eased once it lost focus")
	}

	// Focused again while eased: uresourced replaces Atlas's weight. Atlas lets
	// it go, and when the game calms down puts nothing back over it.
	r.w.w["app-game-1.scope"] = 300
	r.run(5 * time.Second)
	if r.status("game") == EasedAuto {
		t.Fatal("still counted as eased after something else took the weight")
	}
	if _, listed := r.saved["app-game-1.scope"]; listed {
		t.Error("still in crash recovery, which would put Atlas's idea back after a restart")
	}
	r.units.load["app-game-1.scope"] = 1
	r.run(2 * time.Minute)
	if r.w.w["app-game-1.scope"] != 300 {
		t.Fatalf("weight = %d; the focus boost must be left alone", r.w.w["app-game-1.scope"])
	}
}

// TestSomeoneElsesWeightIsNotOurs: a unit set lower by the user or another tool
// is not Atlas's to ease or to put back.
func TestSomeoneElsesWeightIsNotOurs(t *testing.T) {
	r := newRig(t, "app-index-1.scope")
	r.units.load["app-index-1.scope"] = 90
	r.w.w["app-index-1.scope"] = 50
	r.run(2 * time.Minute)
	if r.w.w["app-index-1.scope"] != 50 || r.status("index") != KeptOther {
		t.Fatalf("weight %d, status %v", r.w.w["app-index-1.scope"], r.status("index"))
	}
}

// TestSoundIsKnownBeforeEasing: a busy application that is playing is reported
// as left alone for its sound within one check, not as about to be eased.
func TestSoundIsKnownBeforeEasing(t *testing.T) {
	r := newRig(t, "app-music-1.scope")
	r.units.load["app-music-1.scope"] = 80
	r.audio.playing["music"] = true
	r.run(audioEvery + 5*time.Second)
	if r.status("music") != KeptSound {
		t.Fatalf("status after %v = %v, want KeptSound", audioEvery+5*time.Second, r.status("music"))
	}
	// A quiet machine is never checked at all.
	q := newRig(t, "app-editor-1.scope")
	q.units.load["app-editor-1.scope"] = 3
	q.run(5 * time.Minute)
	if q.audio.calls != 0 {
		t.Errorf("%d sound checks with nothing busy", q.audio.calls)
	}
}
