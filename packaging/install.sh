#!/usr/bin/env bash
# Install Atlas Monitor from a release tarball. No Go toolchain needed — the
# binary is already built; only the GTK4 runtime libraries have to be present.
#
#   ./install.sh            install into ~/.local
#   ./install.sh --system   install into /usr/local (needs root)
#   ./install.sh --uninstall
set -euo pipefail

APPID=com.atlas.Monitor
BINARY=atlas-monitor

# Every symbolic icon the app asks GTK for. This list went stale once already —
# it still named five icons after the set had grown, so a tarball install came
# up with broken images where most of the sidebar should be. Keep it in step
# with ICONS in the Makefile; TestPackagingListsAgree checks that it is.
ICONS="cpu memory disk gpu network wifi battery apps services settings update trash reset menu warning startup"
PREFIX="${PREFIX:-$HOME/.local}"
action=install

for arg in "$@"; do
    case "$arg" in
        --system)    PREFIX=/usr/local ;;
        --uninstall) action=uninstall ;;
        --prefix=*)  PREFIX="${arg#--prefix=}" ;;
        -h|--help)   sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *)           echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done

here="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
DATADIR="$PREFIX/share/atlas-monitor"
APPDIR="$PREFIX/share/applications"
ICONDIR="$PREFIX/share/icons/hicolor/scalable/apps"
ICONACT="$PREFIX/share/icons/hicolor/scalable/actions"

refresh_caches() {
    update-desktop-database "$APPDIR" 2>/dev/null || true
    gtk4-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
}

if [ "$action" = uninstall ]; then
    rm -f "$PREFIX/bin/$BINARY" "$APPDIR/$APPID.desktop" "$ICONDIR/$APPID.svg"
    for icon in $ICONS; do rm -f "$ICONACT/atlas-$icon-symbolic.svg"; done
    rm -rf "$DATADIR"
    refresh_caches
    echo "Removed Atlas Monitor from $PREFIX."
    exit 0
fi

# The binary is dynamically linked against GTK4 and libadwaita. Ask the dynamic
# linker what it cannot resolve rather than guessing at library names, and say so
# now instead of letting the app fail at launch. If ldd is unavailable we simply
# skip the check — it is a courtesy, not a gate.
if command -v ldd >/dev/null 2>&1; then
    missing="$(ldd "$here/$BINARY" 2>/dev/null | awk '/not found/ {print $1}' | sort -u)"
    if [ -n "$missing" ]; then
        echo "This build needs libraries your system does not have:" >&2
        echo "$missing" | sed 's/^/  /' >&2
        echo >&2
        echo "On Fedora:  sudo dnf install gtk4 libadwaita" >&2
        echo "On Debian:  sudo apt install libgtk-4-1 libadwaita-1-0" >&2
        echo >&2
        echo "If they are installed and still listed, this tarball was built" >&2
        echo "against a newer distribution — build from source with 'make install'." >&2
        exit 1
    fi
fi

install -Dm755 "$here/$BINARY"            "$PREFIX/bin/$BINARY"
install -Dm644 "$here/assets/style.css"   "$DATADIR/style.css"
install -Dm644 "$here/assets/icon.svg"    "$ICONDIR/$APPID.svg"
for icon in $ICONS; do
    install -Dm644 "$here/assets/icons/atlas-$icon-symbolic.svg" "$ICONACT/atlas-$icon-symbolic.svg"
done
install -d "$APPDIR"
sed "s|@BIN@|$PREFIX/bin/$BINARY|g" "$here/assets/$APPID.desktop" > "$APPDIR/$APPID.desktop"
chmod 644 "$APPDIR/$APPID.desktop"
refresh_caches

# No source checkout here, so the in-app updater correctly reports that it does
# not know where to pull from; re-run this script with a newer tarball instead.
echo "Installed Atlas Monitor to $PREFIX — press Super and search 'Atlas'."
