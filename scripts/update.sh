#!/usr/bin/env bash
# Update Atlas Monitor from GitHub, then reinstall. Invoked by the in-app
# "Update and restart" button with the chosen channel branch:
#   main = Release (stable), beta = newest features/fixes.
#
# Safety: a dirty working tree is never touched (it just rebuilds in place), the
# pull is fast-forward-only (local commits are never discarded), and a non-git
# source (e.g. a download) falls back to rebuilding what is there. Whatever
# happens, it ends by reinstalling so the app comes back working.
set -uo pipefail

branch="${1:-main}"
cd "$(dirname "$(readlink -f "$0")")/.." || exit 1   # repo root, relative to this script

log=/tmp/atlas-monitor-update.log
exec >"$log" 2>&1
echo "Atlas Monitor update — channel '$branch' — $(date)"

reinstall() { make install; }

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
