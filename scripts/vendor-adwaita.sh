#!/usr/bin/env bash
# Rebuild third_party/gotk4-adwaita from upstream, with the one change Atlas
# needs made to it.
#
# Why this exists: gotk4 0.4.1 fixed the GObject lifecycle — closures and the
# objects they belong to are finally released — and in doing so made signal
# closures deletable during object cleanup. A signal that arrives in that window
# finds no closure, and every generated trampoline was regenerated to return
# quietly when that happens instead of panicking. gotk4-adwaita's latest code was
# generated with gotk4 0.4.0, before that change, so its trampolines still panic:
# a panic inside a GTK signal handler, which takes the whole application down.
#
# The fix is exactly what regenerating would produce — gotk4 0.4.0 → 0.4.1
# changed that one line in all 983 of its own generated trampolines and nothing
# else in generated code — so this applies the same edit to gotk4-adwaita's.
# When upstream regenerates with gotk4 0.4.1 or later, delete third_party/ and
# the replace line in go.mod, and require the new version instead.
#
# Usage: scripts/vendor-adwaita.sh [version]   (default: the one go.mod requires)
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."

mod=github.com/diamondburned/gotk4-adwaita/pkg
version=${1:-$(go mod edit -json | python3 -c '
import json, sys
for r in json.load(sys.stdin)["Require"]:
    if r["Path"] == "github.com/diamondburned/gotk4-adwaita/pkg":
        print(r["Version"])')}
gotk4=$(go mod edit -json | python3 -c '
import json, sys
for r in json.load(sys.stdin)["Require"]:
    if r["Path"] == "github.com/diamondburned/gotk4/pkg":
        print(r["Version"])')

src=$(GOFLAGS=-mod=mod go mod download -json "$mod@$version" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')
dst=third_party/gotk4-adwaita
rm -rf "$dst"
mkdir -p "$dst"
cp -r "$src"/. "$dst"/
chmod -R u+w "$dst"
rm -rf "$dst/_examples" # never compiled: Go skips directories starting with _

# The guard, and the gotk4 it is built against.
grep -rl 'panic("given unknown closure user_data")' "$dst" |
    xargs sed -i 's/panic("given unknown closure user_data")/return/'
(cd "$dst" && go mod edit -require="github.com/diamondburned/gotk4/pkg@$gotk4")

# (grep finding nothing is the success case, and exits 1.)
left=$( (grep -r 'given unknown closure user_data' "$dst" || true) | wc -l)
if [ "$left" -ne 0 ]; then
    echo "still $left unguarded trampolines in $dst" >&2
    exit 1
fi
cat > "$dst/ATLAS.md" <<EOF
# gotk4-adwaita, as Atlas builds it

Upstream $mod@$version, with its generated signal trampolines returning instead
of panicking when a closure has already been released, and built against gotk4
$gotk4. Produced by scripts/vendor-adwaita.sh; see that script for why, and
for when this directory can go.
EOF
echo "vendored $mod@$version against gotk4 $gotk4 into $dst"
