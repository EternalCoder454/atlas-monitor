#!/usr/bin/env bash
# Build Atlas Monitor for Windows and stage everything it needs to run elsewhere.
#
#   ./stage-windows.sh [version]
#
# Run from the repository root inside an MSYS2 MINGW64 shell. Produces
# dist/atlas-monitor-<version>-windows-x86_64/ and a .zip of it beside it.
#
# A GTK application on Windows carries its own world, and none of it is found by
# default. The DLL paths compiled into the GTK libraries point at wherever MSYS2
# sat on the machine that built them; left alone, GLib aborts at startup because it
# cannot find its schemas, and every icon comes up as an empty square because the
# SVG loader is not registered. The exe points GTK at its own folder on startup —
# see internal/app/portable_windows.go — and this puts the folder together.
#
# It lives here rather than inside a workflow because two workflows need it: the
# branch build and the release. A release that packaged differently from what CI
# tested would be the worst possible place for that difference to first appear.
set -euo pipefail

version="${1:-$(cat VERSION)}"
name="atlas-monitor-${version}-windows-x86_64"
dist="dist/$name"

echo "==> building atlas-monitor.exe"
CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -H=windowsgui" -o atlas-monitor.exe .

rm -rf "$dist"
mkdir -p "$dist"
cp atlas-monitor.exe "$dist/"

echo "==> collecting DLLs"
# Taken from what the linker actually recorded rather than a hand-kept list. ldd
# names the mingw64 ones by absolute path; the system ones under /c/WINDOWS are
# already on the machine and must not be shipped.
ldd atlas-monitor.exe | awk '/=> \/mingw64/ {print $3}' | sort -u > /tmp/atlas-dll-all.txt
echo "    directly linked: $(wc -l < /tmp/atlas-dll-all.txt)"

# ...and what those need in turn. GTK pulls in a deep tree — Pango, cairo,
# HarfBuzz, GLib, the lot — so walk it to a fixed point rather than guessing a
# depth. Six rounds is a bound, not an expectation; it settles in three or four.
for round in 1 2 3 4 5 6; do
    before=$(wc -l < /tmp/atlas-dll-all.txt)
    while read -r dll; do
        ldd "$dll" 2>/dev/null | awk '/=> \/mingw64/ {print $3}'
    done < /tmp/atlas-dll-all.txt | sort -u > /tmp/atlas-dll-next.txt
    sort -u /tmp/atlas-dll-all.txt /tmp/atlas-dll-next.txt > /tmp/atlas-dll-merged.txt
    mv /tmp/atlas-dll-merged.txt /tmp/atlas-dll-all.txt
    after=$(wc -l < /tmp/atlas-dll-all.txt)
    echo "    round $round: $before -> $after"
    [ "$before" != "$after" ] || break
done
xargs -a /tmp/atlas-dll-all.txt -r cp -t "$dist"
echo "    shipping $(wc -l < /tmp/atlas-dll-all.txt) DLLs"

echo "==> compiling GSettings schemas"
# GLib aborts on startup if it cannot find these, and libadwaita's own settings
# are among them.
mkdir -p "$dist/share/glib-2.0/schemas"
cp /mingw64/share/glib-2.0/schemas/*.xml "$dist/share/glib-2.0/schemas/" 2>/dev/null || true
glib-compile-schemas "$dist/share/glib-2.0/schemas"
test -f "$dist/share/glib-2.0/schemas/gschemas.compiled" \
    || { echo "schemas did not compile; GTK would abort at startup" >&2; exit 1; }

echo "==> staging icons"
mkdir -p "$dist/share/icons"
cp -r /mingw64/share/icons/Adwaita "$dist/share/icons/"
cp -r /mingw64/share/icons/hicolor "$dist/share/icons/" 2>/dev/null || true

# Atlas's own symbolic icons. All atlas-prefixed on purpose: icon lookup falls back
# to hicolor last, so a generic name would lose to the theme and never be used.
icondir="$dist/share/icons/hicolor/scalable/actions"
mkdir -p "$icondir" "$dist/share/icons/hicolor/scalable/apps"
cp assets/icons/atlas-*-symbolic.svg "$icondir/"
cp assets/icon.svg "$dist/share/icons/hicolor/scalable/apps/com.atlas.Monitor.svg"
gtk4-update-icon-cache -f -t "$dist/share/icons/hicolor" || true

echo "==> staging gdk-pixbuf loaders"
# The generated cache holds absolute build-machine paths, so it is generated with
# MODULEDIR set to the relative path the exe will point at instead.
loaders="lib/gdk-pixbuf-2.0/2.10.0/loaders"
mkdir -p "$dist/$loaders"
cp /mingw64/$loaders/*.dll "$dist/$loaders/"
( cd "$dist" && GDK_PIXBUF_MODULEDIR="$loaders" \
    gdk-pixbuf-query-loaders > lib/gdk-pixbuf-2.0/2.10.0/loaders.cache )

# The SVG loader is the one that matters. Without it every icon in the sidebar is a
# blank square — an app that starts, looks broken, and gives no clue why. Better
# caught here than in somebody's download.
grep -qi 'svg' "$dist/lib/gdk-pixbuf-2.0/2.10.0/loaders.cache" \
    || { echo "no SVG loader registered; every icon would be blank" >&2; exit 1; }
echo "    SVG loader present"

cp README.md LICENSE NOTICE "$dist/"

echo "==> zipping"
( cd dist && zip -qr "$name.zip" "$name" && sha256sum "$name.zip" > "$name.zip.sha256" )
ls -l "dist/$name.zip"
echo "==> done: dist/$name.zip"
