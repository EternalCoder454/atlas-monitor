#!/usr/bin/env bash
# Update Atlas Monitor from GitHub, then reinstall. Invoked by the in-app
# "Update and restart" button as:  update.sh <branch> [prefix]
#   main = Release (stable), beta = newest features/fixes.
#   prefix defaults to the Makefile's own ($HOME/.local).
#
# Safety: a dirty working tree is never touched (it just rebuilds in place), the
# pull is fast-forward-only (local commits are never discarded), and a non-git
# source (e.g. a download) falls back to rebuilding what is there. Whatever
# happens, it ends by reinstalling so the app comes back working.
set -uo pipefail

branch="${1:-main}"
# Where to install the result. The caller passes the prefix the running copy was
# installed under, so a rebuild replaces it instead of landing somewhere else and
# leaving two Atlases for PATH to choose between. Empty means the Makefile's own
# default, which is what a person running this script by hand would expect.
prefix="${2:-}"
cd "$(dirname "$(readlink -f "$0")")/.." || exit 1   # repo root, relative to this script

# The log goes under the user's own state directory rather than a predictable
# name in /tmp. A shared world-writable path can be squatted by another user on
# a multi-user machine, and because a failed redirect at exec is fatal to the
# shell, that would stop updates from running at all.
state="${XDG_STATE_HOME:-$HOME/.local/state}/atlas-monitor"
mkdir -p "$state" 2>/dev/null || state="$(mktemp -d)"
log="$state/update.log"
exec >"$log" 2>&1
echo "Atlas Monitor update — channel '$branch' — $(date)"

# Rebuild with the same build tags the install was made with.
tagfile="${XDG_DATA_HOME:-$HOME/.local/share}/atlas-monitor/buildtags"
tags=""
[ -f "$tagfile" ] && tags="$(cat "$tagfile")"

reinstall() {
    if [ -n "$prefix" ]; then
        make install TAGS="$tags" PREFIX="$prefix"
    else
        make install TAGS="$tags"
    fi
}

# Not a git checkout (tarball/zip): nothing to pull, just rebuild.
if ! git rev-parse --git-dir >/dev/null 2>&1; then
    echo "Source is not a git checkout; rebuilding the current source."
    reinstall
    exit $?
fi

# Never clobber uncommitted work — rebuild in place instead of pulling.
if [ -n "$(git status --porcelain)" ]; then
    echo "Working tree has local changes; skipping the GitHub pull and rebuilding in place."
    reinstall
    exit $?
fi

echo "Fetching origin…"
if ! git fetch --prune origin; then
    echo "git fetch failed (offline?); rebuilding the current source."
    reinstall
    exit $?
fi

# Switch to the channel branch (git auto-creates a tracking branch from
# origin/<branch> on first use). If it can't switch, stay put and rebuild.
if ! git checkout "$branch"; then
    echo "Cannot switch to '$branch'; staying on $(git rev-parse --abbrev-ref HEAD) and rebuilding."
    reinstall
    exit $?
fi

# Fast-forward only: succeeds (or is a no-op) for a clean follower; refuses to
# rewrite a diverged local branch.
if git merge --ff-only "origin/$branch"; then
    echo "Updated to $(git rev-parse --short HEAD) on $branch."
else
    echo "Local '$branch' has diverged from origin/$branch; not fast-forwarding. Rebuilding current state."
fi

reinstall
