package process

// What "ease off" means, on any platform.
//
// The Energy Saver page offers to put a program behind everything else on the
// processor. Both platforms can do that; they disagree about whether it can be
// undone, which is why EaseOffReversible is per-platform and the page reads its
// wording from it rather than stating one of the two answers.

// EasedNice is where "ease off" puts a program: clearly behind everything at the
// default of zero, without starving it the way the very bottom would.
//
// It is expressed as a Unix nice value because that is the scale the UI already
// speaks. Windows has priority classes rather than a number, and maps this onto
// the one that means the same thing — see nice_windows.go.
const EasedNice = 10
