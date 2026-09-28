// Package ease is Energy Saver's automatic half: it notices an application
// that has kept the processor busy for a while and puts it behind everything
// else, and puts it back when it calms down — or the moment it starts playing
// or recording sound.
//
// The mechanism is the application's CPU weight, set on its systemd unit
// through the user's own service manager. That matters for two reasons. It is
// reversible: lowering a process's nice value, which is what the manual
// button did, cannot be undone without root, and an automatic mode that can
// never take back a decision would be a liability. And it acts on the whole
// application, every process it has and every one it starts later, which is
// what a person means by "ease off Firefox".
//
// Weight only matters when something else wants the processor. An eased
// application on an idle machine runs exactly as fast as before; what changes
// is who wins when two things want the same core. That is also why sound is
// safe even before the audio check notices it: nothing is ever starved, only
// asked to wait its turn behind a lighter neighbour.
package ease

import (
	"sort"
	"sync"
	"time"
)

// Policy constants. An application has to be busy for easeAfter before it is
// touched, so a burst — a page load, a build step — never is; and calm for
// restoreAfter before it is put back, so one that pulses does not flap.
const (
	// EasedWeight is the CPU weight an eased application gets, against the
	// default of 100: roughly a tenth of a share when it competes with a
	// normal one, never nothing.
	EasedWeight = 10

	heavyPercent = 50 // of one core, averaged over an interval
	calmPercent  = 15
	easeAfter    = 30 * time.Second
	restoreAfter = 60 * time.Second
	// audioEvery is how often an application already eased is re-checked for
	// sound. One about to be eased is always checked first.
	audioEvery = 10 * time.Second
)

// Status is what Energy Saver is doing about an application.
type Status int

const (
	Normal Status = iota
	Busy          // heavy, and counting towards easeAfter
	EasedAuto
	EasedManual
	KeptSound    // heavy, but playing or recording
	KeptTerminal // heavy, but it is a terminal
	KeptNever    // heavy, but the user said never
	// KeptInUse is an application something else has raised: on Fedora,
	// uresourced gives the focused application a weight of 300, which makes
	// it the one being used — the last thing to put behind the rest.
	KeptInUse
	// KeptOther is an application whose weight something else has set lower
	// or differently. Whoever did that is in charge of it.
	KeptOther
)

// App is one application as the Energy Saver page shows it.
type App struct {
	ID   string
	Name string
	// Unit is one of the application's units, for looking up its icon.
	Unit   string
	CPU    float64 // percent of one core, over the last interval
	Status Status
	// Since is when it was eased, for the eased statuses.
	Since time.Time
}

// Sample is one unit's reading: its cumulative CPU time in microseconds, and
// the weight the kernel is applying to it now (0 when it cannot be read).
type Sample struct {
	Usage  uint64
	Weight uint64
}

// DefaultWeight is the kernel's weight for a cgroup nobody has set one on.
const DefaultWeight = 100

// Units reads every application unit. It is the cgroup tree on Linux, and a
// fake in the tests.
type Units interface {
	Sample() (map[string]Sample, error)
}

// Weights reads and sets a unit's CPU weight. Unset is the service manager's
// "not set, use the default" value, which is what an untouched unit reads as.
type Weights interface {
	Weight(unit string) (uint64, error)
	SetWeight(unit string, w uint64) error
}

// Unset is systemd's "no weight configured" for CPUWeight.
const Unset = ^uint64(0)

// Audio says which applications are playing or recording right now, by ID.
type Audio interface {
	AudibleApps() (map[string]bool, error)
}

// Identity turns a unit into its application: its ID, its name, and whether
// it is a terminal. ok is false for anything that is not an application's.
type Identity func(unit string) (id, name string, terminal bool, ok bool)

// Controller holds the state of every application seen and carries out the
// policy. Every method is safe to call from any goroutine.
type Controller struct {
	units   Units
	weights Weights
	audio   Audio
	ident   Identity
	now     func() time.Time
	// self is Atlas's own application ID, which it never eases.
	self string
	// save is told the automatic eases after every change, for crash recovery.
	save func(map[string]uint64)

	mu        sync.Mutex
	automatic bool
	never     map[string]bool
	apps      map[string]*appState
	last      time.Time
	lastAudio time.Time
	audible   map[string]bool
}

type appState struct {
	id, name string
	terminal bool
	units    map[string]*unitState
	cpu      float64
	heavyFor time.Duration
	calmFor  time.Duration
	eased    bool
	manual   bool
	since    time.Time
	// raised and foreign say whether something other than Atlas has set a
	// weight on any of the application's units: raised above the default, or
	// set otherwise. Either way it is not Atlas's to touch.
	raised, foreign bool
}

type unitState struct {
	usage  uint64
	weight uint64 // as the kernel last reported it
	fresh  bool   // first sample: no delta yet
	eased  bool
	prev   uint64 // the weight to put back
}

// New returns a controller. It does nothing until Tick is called.
func New(units Units, weights Weights, audio Audio, ident Identity, self string) *Controller {
	return &Controller{
		units: units, weights: weights, audio: audio, ident: ident, self: self,
		now:   time.Now,
		never: map[string]bool{},
		apps:  map[string]*appState{},
	}
}

// SetAutomatic turns the automatic half on or off. Off puts back everything it
// eased by itself; what the user eased by hand stays eased.
func (c *Controller) SetAutomatic(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.automatic = on
	if !on {
		for _, a := range c.apps {
			if a.eased && !a.manual {
				c.restore(a)
			}
		}
		c.persist()
	}
}

// SetNever replaces the list of applications never to ease automatically, by
// ID. One already eased automatically is put back at once.
func (c *Controller) SetNever(ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.never = map[string]bool{}
	for _, id := range ids {
		c.never[id] = true
	}
	for _, a := range c.apps {
		if a.eased && !a.manual && c.never[a.id] {
			c.restore(a)
		}
	}
	c.persist()
}

// Ease puts an application behind the rest at the user's request. It stays
// eased until Restore, whatever it does in the meantime.
func (c *Controller) Ease(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.apps[id]
	if a == nil {
		return nil
	}
	err := c.ease(a)
	a.manual = true
	c.persist()
	return err
}

// Restore puts an application back, however it was eased.
func (c *Controller) Restore(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.apps[id]
	if a == nil {
		return nil
	}
	err := c.restore(a)
	c.persist()
	return err
}

// Shutdown puts back everything eased automatically: once Atlas stops
// watching, nobody would notice an application start playing and restore it.
// What the user eased by hand stays, as a choice they made.
func (c *Controller) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.apps {
		if a.eased && !a.manual {
			c.restore(a)
		}
	}
	c.persist()
}

// Snapshot is every application worth showing, busiest first: anything using
// a noticeable share of a core, and anything eased whatever it is doing.
func (c *Controller) Snapshot() []App {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []App
	for _, a := range c.apps {
		if a.cpu < 5 && !a.eased {
			continue
		}
		app := App{ID: a.id, Name: a.name, CPU: a.cpu, Status: c.status(a), Since: a.since}
		for u := range a.units {
			if app.Unit == "" || u < app.Unit {
				app.Unit = u
			}
		}
		out = append(out, app)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPU != out[j].CPU {
			return out[i].CPU > out[j].CPU
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (c *Controller) status(a *appState) Status {
	switch {
	case a.eased && a.manual:
		return EasedManual
	case a.eased:
		return EasedAuto
	case a.cpu < heavyPercent:
		return Normal
	case a.raised:
		return KeptInUse
	case a.foreign:
		return KeptOther
	case c.never[a.id]:
		return KeptNever
	case a.terminal:
		return KeptTerminal
	case c.audible[a.id]:
		return KeptSound
	}
	return Busy
}

// Tick takes a sample and acts on it. It is called every few seconds, whether
// or not Atlas's window is open: this is the one part of Atlas that works in
// the background, and it costs a small file read per application.
func (c *Controller) Tick() {
	usage, err := c.units.Sample()
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	dt := now.Sub(c.last)
	first := c.last.IsZero()
	c.last = now

	// Fold units into applications.
	seen := map[string]bool{}
	for unit, smp := range usage {
		id, name, terminal, ok := c.ident(unit)
		if !ok {
			continue
		}
		a := c.apps[id]
		if a == nil {
			a = &appState{id: id, name: name, terminal: terminal, units: map[string]*unitState{}}
			c.apps[id] = a
		}
		seen[id] = true
		u := a.units[unit]
		if u == nil {
			u = &unitState{usage: smp.Usage, weight: smp.Weight, fresh: true}
			a.units[unit] = u
			// A new window of an eased application joins it.
			if a.eased {
				c.easeUnit(unit, u)
			}
			continue
		}
		u.fresh = false
	}

	// CPU per application over the interval, and forget what has gone. An
	// eased unit that has ended is a change too: the crash-recovery file must
	// stop listing it.
	changed := false
	for id, a := range c.apps {
		if !seen[id] {
			changed = changed || a.eased
			delete(c.apps, id)
			continue
		}
		var busy uint64
		a.raised, a.foreign = false, false
		for unit, u := range a.units {
			smp, ok := usage[unit]
			if !ok {
				changed = changed || u.eased
				delete(a.units, unit) // that window closed
				continue
			}
			if !u.fresh && smp.Usage >= u.usage {
				busy += smp.Usage - u.usage
			}
			u.usage, u.weight = smp.Usage, smp.Weight
			// Someone else has changed a weight Atlas set — uresourced raising
			// the application that just got focus, or the user. Theirs wins:
			// the unit is no longer Atlas's, and nothing is put back over it.
			if u.eased && u.weight != 0 && u.weight != EasedWeight {
				u.eased = false
				changed = true
			}
			if !u.eased && u.weight != 0 && u.weight != DefaultWeight {
				a.foreign = true
				a.raised = a.raised || u.weight > DefaultWeight
			}
		}
		if a.eased && !anyEased(a) {
			a.eased, a.manual = false, false
			a.heavyFor = 0 // start counting again, from now
		}
		a.cpu = 0
		if !first && dt > 0 {
			a.cpu = float64(busy) / float64(dt.Microseconds()) * 100
		}
		switch {
		case a.cpu >= heavyPercent:
			a.heavyFor += dt
			a.calmFor = 0
		case a.cpu < calmPercent:
			a.calmFor += dt
			a.heavyFor = 0
		default:
			a.heavyFor, a.calmFor = 0, 0
		}
	}

	if !c.automatic {
		if changed {
			c.persist()
		}
		return
	}

	// Who is due to be eased, and is a sound check due? One is always made
	// before easing anything, and every audioEvery while anything is eased
	// automatically or busy — the first so an eased application is put back
	// soon after it starts playing, the second so the page can say a busy one
	// is being left alone for its sound rather than that it is about to be
	// eased.
	var due []*appState
	checkSound := now.Sub(c.lastAudio) >= audioEvery
	watch := false
	for _, a := range c.apps {
		if a.eased {
			watch = watch || !a.manual
			continue
		}
		watch = watch || a.cpu >= heavyPercent
		if a.heavyFor >= easeAfter && !c.never[a.id] && !a.terminal && a.id != c.self && !a.foreign {
			due = append(due, a)
		}
	}
	if len(due) > 0 || (watch && checkSound) {
		if aud, err := c.audio.AudibleApps(); err == nil {
			c.audible = aud
			c.lastAudio = now
		} else if len(due) > 0 {
			// Without knowing what is playing, easing anything could be the
			// one thing this must never do. Wait for the next tick.
			due = nil
		}
	}

	for _, a := range due {
		if c.audible[a.id] {
			continue
		}
		c.ease(a)
		changed = true
	}
	for _, a := range c.apps {
		if !a.eased || a.manual {
			continue
		}
		if a.calmFor >= restoreAfter || c.audible[a.id] || c.never[a.id] {
			c.restore(a)
			changed = true
		}
	}
	if changed {
		c.persist()
	}
}

// ease lowers every unit of an application. Called with the lock held.
func (c *Controller) ease(a *appState) error {
	var firstErr error
	for unit, u := range a.units {
		if err := c.easeUnit(unit, u); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Eased is whatever the units say: if the service manager refused one, the
	// application is only as eased as the units it accepted.
	was := a.eased
	a.eased = anyEased(a)
	if a.eased && !was {
		a.since = c.now()
	}
	return firstErr
}

func anyEased(a *appState) bool {
	for _, u := range a.units {
		if u.eased {
			return true
		}
	}
	return false
}

func (c *Controller) easeUnit(unit string, u *unitState) error {
	if u.eased {
		return nil
	}
	prev, err := c.weights.Weight(unit)
	if err != nil {
		return err
	}
	if err := c.weights.SetWeight(unit, EasedWeight); err != nil {
		return err
	}
	u.eased, u.prev = true, prev
	return nil
}

// restore puts every unit of an application back to the weight it had.
func (c *Controller) restore(a *appState) error {
	var firstErr error
	for unit, u := range a.units {
		if !u.eased {
			continue
		}
		// Only over Atlas's own weight. If something changed it since, that
		// change stands.
		if cur, err := c.weights.Weight(unit); err == nil && cur != EasedWeight {
			u.eased = false
			continue
		}
		if err := c.weights.SetWeight(unit, u.prev); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		u.eased = false
	}
	a.eased = anyEased(a)
	if !a.eased {
		a.manual = false
	}
	a.heavyFor, a.calmFor = 0, 0
	return firstErr
}

// persist hands the automatic eases to the saver: unit → weight to restore.
func (c *Controller) persist() {
	if c.save == nil {
		return
	}
	out := map[string]uint64{}
	for _, a := range c.apps {
		if !a.eased || a.manual {
			continue
		}
		for unit, u := range a.units {
			if u.eased {
				out[unit] = u.prev
			}
		}
	}
	c.save(out)
}

// OnChange sets the function told about automatic eases after each change.
func (c *Controller) OnChange(save func(map[string]uint64)) {
	c.mu.Lock()
	c.save = save
	c.mu.Unlock()
}
