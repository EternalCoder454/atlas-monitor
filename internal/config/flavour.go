package config

// This build's flavour. Atlas is one application with three channels to follow,
// and the minimal branch is the one whose build leaves the assistant out: there
// this file says so, and makes its own channel the default. Everything else
// about channels is shared, so switching from one to another is only a matter
// of following a different branch.
const (
	// Minimal is whether this is the build without the assistant.
	Minimal = true
	// DefaultChannel is the channel a fresh install follows.
	DefaultChannel = ChannelMinimal
)
