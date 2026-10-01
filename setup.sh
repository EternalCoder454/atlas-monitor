#!/usr/bin/env bash
# Atlas Monitor: install, update or remove it on any Linux distribution.
#
#   curl -fsSL https://raw.githubusercontent.com/EternalCoder454/atlas-monitor/main/setup.sh | bash
#
# or, from a copy of this file or a checkout:
#
#   bash setup.sh                 install (the default)
#   bash setup.sh update          update an installed copy
#   bash setup.sh uninstall       remove it; add --purge to remove settings too
#   bash setup.sh check           say what this machine has and what it needs
#
# Options:
#   --beta          follow the Beta channel instead of Release
#   --minimal       follow the Minimal channel: Atlas without the AI assistant
#   --yes           do not ask before installing packages or removing things
#   --container     build in a Fedora container even if this system could build natively
#   --native        never use a container; stop if the system libraries are too old
#   --purge         with uninstall: also remove settings, logs and caches
#
# Atlas needs GTK 4.22 and libadwaita 1.9, which it is built against. Where the
# distribution ships those — Fedora 44, Arch and its family, openSUSE
# Tumbleweed, Ubuntu 26.04, Linux Mint 23, Debian testing — it is built from
# source with the distribution's own packages. Where it ships older ones —
# Debian 13, Ubuntu 24.04 and 25.x, Linux Mint 22 — it is built and run in a
# small Fedora container through distrobox instead. The container shares your
# home, your processes and your desktop, so Atlas sees the real machine, and it
# appears in the app menu like anything else.
#
# Nothing here writes outside your home directory except through your package
# manager, which asks for your password itself.
set -euo pipefail

REPO="https://github.com/EternalCoder454/atlas-monitor"
PREFIX="$HOME/.local"
# Where make install keeps its own files; the Makefile fixes it under PREFIX.
DATA="$PREFIX/share/atlas-monitor"
SRC="$DATA/src"
BOX="atlas-monitor"
BOX_IMAGE="registry.fedoraproject.org/fedora:44"
NEED_GTK="4.22"
NEED_ADW="1.9"
NEED_GO="1.24"

action="install"
channel=""
yes=0
route="auto"
purge=0

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
note() { printf '  %s\n' "$*"; }
die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,24p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; }

for arg in "$@"; do
	case "$arg" in
	install | update | uninstall | check) action="$arg" ;;
	--beta) channel="beta" ;;
	--minimal) channel="minimal" ;;
	--release) channel="main" ;;
	--yes | -y) yes=1 ;;
	--container) route="container" ;;
	--native) route="native" ;;
	--purge) purge=1 ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown option '$arg' (try --help)" ;;
	esac
done

# ask QUESTION: yes unless the answer is no. Piped from curl, stdin is this
# script, so the answer is read from the terminal instead.
ask() {
	[ "$yes" = 1 ] && return 0
	local reply=""
	# /dev/tty exists without a terminal behind it (ssh without -t, cron);
	# opening it is the test that tells.
	if { : </dev/tty; } 2>/dev/null; then
		printf '%s [Y/n] ' "$1" >/dev/tty
		# No answer at all — the terminal closed — is not a yes.
		read -r reply </dev/tty || return 1
	else
		die "$1 — rerun with --yes to answer yes without a terminal"
	fi
	case "$reply" in [nN]*) return 1 ;; *) return 0 ;; esac
}

# as_root runs a command with the privileges packages need.
as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	elif command -v sudo >/dev/null; then
		sudo "$@"
	elif command -v doas >/dev/null; then
		doas "$@"
	else
		die "installing packages needs root, and neither sudo nor doas is here: run '$*' as root, then this again"
	fi
}

have() { command -v "$1" >/dev/null 2>&1; }

# version_ge A B: whether version A is at least B.
version_ge() { [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]; }

distro_name() {
	if [ -r /etc/os-release ]; then
		# shellcheck disable=SC1091
		(. /etc/os-release && printf '%s' "${PRETTY_NAME:-${NAME:-Linux}}")
	else
		printf 'this Linux'
	fi
}

# --- Packages -----------------------------------------------------------------

# The package manager, and what it calls everything a native build needs.
pm="" packages="" install_cmd=()
detect_pm() {
	if have pacman; then
		pm="pacman"; packages="go gtk4 libadwaita gobject-introspection base-devel git pkgconf"
		install_cmd=(pacman -S --needed --noconfirm)
	elif have apt-get; then
		pm="apt"; packages="golang-go libgtk-4-dev libadwaita-1-dev libgirepository1.0-dev build-essential pkg-config git"
		install_cmd=(apt-get install -y)
	elif have dnf; then
		pm="dnf"; packages="golang gtk4-devel libadwaita-devel gobject-introspection-devel gcc pkgconf-pkg-config git make"
		install_cmd=(dnf install -y)
	elif have zypper; then
		pm="zypper"; packages="go gtk4-devel libadwaita-devel gobject-introspection-devel gcc pkg-config git make"
		install_cmd=(zypper --non-interactive install)
	elif have xbps-install; then
		pm="xbps"; packages="go gtk4-devel libadwaita-devel gobject-introspection gcc pkg-config git make"
		install_cmd=(xbps-install -Sy)
	elif have apk; then
		pm="apk"; packages="go gtk4.0-dev libadwaita-dev gobject-introspection-dev build-base pkgconf git"
		install_cmd=(apk add)
	elif have eopkg; then
		pm="eopkg"; packages="golang libgtk-4-devel libadwaita-devel gobject-introspection-devel pkg-config git make gcc"
		install_cmd=(eopkg install -y)
	else
		pm=""
	fi
}

# What a native build is missing, one short reason per line; empty when nothing.
missing_native() {
	have git || echo "git"
	have make || echo "make"
	{ have cc || have gcc || have clang; } || echo "a C compiler"
	{ have pkg-config || have pkgconf; } || echo "pkg-config"
	if have go; then
		local v
		v="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
		# Go 1.21 and later fetch the toolchain go.mod asks for by themselves.
		version_ge "${v:-0}" "1.21" || echo "Go $NEED_GO or newer (this one is $v)"
	else
		echo "Go"
	fi
	if have pkg-config || have pkgconf; then
		pkg-config --exists gtk4 || echo "the GTK 4 development files"
		pkg-config --exists libadwaita-1 || echo "the libadwaita development files"
		# The GTK bindings read GLib's error types through it.
		pkg-config --exists gobject-introspection-1.0 || echo "the GObject introspection development files"
	fi
}

# Why the system's libraries cannot build Atlas, or nothing when they can.
too_old() {
	local gtk adw
	gtk="$(pkg-config --modversion gtk4 2>/dev/null || true)"
	adw="$(pkg-config --modversion libadwaita-1 2>/dev/null || true)"
	[ -n "$gtk" ] && ! version_ge "$gtk" "$NEED_GTK" && echo "GTK $gtk (Atlas needs $NEED_GTK)"
	[ -n "$adw" ] && ! version_ge "$adw" "$NEED_ADW" && echo "libadwaita $adw (Atlas needs $NEED_ADW)"
	return 0
}

install_packages() {
	[ -n "$pm" ] || die "no package manager this script knows. Install Go $NEED_GO+, a C compiler, pkg-config, git, make, GTK $NEED_GTK+ and libadwaita $NEED_ADW+ development files, then run this again."
	say "Installing what the build needs with $pm:"
	note "${install_cmd[*]} $packages"
	ask "Go ahead?" || die "nothing installed"
	# shellcheck disable=SC2086
	if [ "$pm" = "apt" ]; then
		as_root apt-get update
	fi
	# shellcheck disable=SC2086
	as_root "${install_cmd[@]}" $packages
}

# --- Native install --------------------------------------------------------------

installed_channel() {
	local b=""
	[ -d "$SRC/.git" ] && b="$(git -C "$SRC" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
	# A detached checkout has no branch to follow; Release is the default.
	[ -z "$b" ] || [ "$b" = HEAD ] && b="main"
	printf '%s' "$b"
}

fetch_source() {
	local want="$1"
	if [ -d "$SRC/.git" ]; then
		say "Updating the source in $SRC"
		if [ -n "$(git -C "$SRC" status --porcelain)" ]; then
			note "It has local changes, so it is rebuilt as it is rather than pulled."
			return
		fi
		git -C "$SRC" fetch --quiet origin "$want" || die "couldn't fetch '$want' from GitHub (offline?)"
		git -C "$SRC" checkout --quiet "$want" 2>/dev/null || git -C "$SRC" checkout --quiet -b "$want" "origin/$want"
		git -C "$SRC" merge --quiet --ff-only "origin/$want" ||
			die "$SRC has commits of its own that GitHub does not, so it cannot simply be moved forward; sort that out with git, or remove it and install again"
	else
		say "Downloading the source to $SRC"
		mkdir -p "$DATA"
		rm -rf "$SRC"
		git clone --quiet --branch "$want" "$REPO.git" "$SRC"
	fi
}

build_and_install() {
	local want="$1" launcher="${2:-}"
	local v
	v="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
	if ! version_ge "${v:-0}" "$NEED_GO"; then
		# Older Go downloads the version go.mod names; some distributions turn
		# that off by default.
		note "Go $v is older than $NEED_GO; Go will fetch $NEED_GO for this build."
		export GOTOOLCHAIN=auto
	fi
	fetch_source "$want"
	mkdir -p "$DATA"
	if [ -n "$launcher" ]; then
		printf '%s\n' "$launcher" >"$DATA/launcher"
	else
		rm -f "$DATA/launcher"
	fi
	say "Building Atlas Monitor (the first build takes a few minutes)"
	make -C "$SRC" install PREFIX="$PREFIX"
	# Settings names the channel this copy follows; after installing another
	# one it has to say so, or it would offer to switch back.
	local conf="${XDG_CONFIG_HOME:-$HOME/.config}/atlas-monitor/settings.json"
	if [ -f "$conf" ]; then
		sed -i "s/\"update_channel\": *\"[a-z]*\"/\"update_channel\": \"$want\"/" "$conf"
	fi
}

native_install() {
	local want="$1"
	local missing
	missing="$(missing_native)"
	if [ -n "$missing" ]; then
		say "This system is missing:"
		printf '%s\n' "$missing" | sed 's/^/  - /'
		install_packages
		missing="$(missing_native)"
		[ -z "$missing" ] || die "still missing after installing packages: $(printf '%s' "$missing" | tr '\n' ',' | sed 's/,$//')"
	fi
	local old
	old="$(too_old)"
	if [ -n "$old" ]; then
		libs_too_old=1
		return 0
	fi
	# Set when the container route runs this inside the container: the app
	# menu entry has to start Atlas through the container.
	build_and_install "$want" "${ATLAS_SETUP_LAUNCHER:-}"
}

# --- Container install -----------------------------------------------------------

# in_box: whether this is the run inside Atlas's own container, which the
# outer run marks by saying how the app menu should start it.
in_box() { [ -n "${ATLAS_SETUP_LAUNCHER:-}" ]; }

# box_exists NAME: whether distrobox has a container of exactly that name.
box_exists() {
	distrobox list --no-color 2>/dev/null | awk -F'|' -v n="$1" 'NR > 1 { gsub(/ /, "", $2); if ($2 == n) found = 1 } END { exit !found }'
}

# known_old names the release when it is one whose GTK or libadwaita is known
# to be too old, so the container can be offered before anything is installed
# on the host only to be found wanting. Ubuntu's derivatives — Mint, Pop!_OS,
# elementary, Zorin — carry the Ubuntu codename they are built on.
known_old() {
	[ -r /etc/os-release ] || return 1
	local code
	# shellcheck disable=SC1091
	code="$(. /etc/os-release && printf '%s' "${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}")"
	case "$code" in
	jammy | noble | oracular | plucky | questing | bookworm | trixie) distro_name ;;
	*) return 1 ;;
	esac
}
box_installed() { [ -f "$DATA/container" ]; }

ensure_distrobox() {
	if have distrobox; then
		return
	fi
	detect_pm
	[ -n "$pm" ] || die "this route needs distrobox (https://distrobox.it) and podman or docker; install them and run this again."
	say "The container route needs distrobox, which $pm can install:"
	local pkgs="distrobox"
	have podman || have docker || pkgs="distrobox podman"
	note "${install_cmd[*]} $pkgs"
	ask "Install it?" || die "nothing installed"
	[ "$pm" = "apt" ] && as_root apt-get update
	# shellcheck disable=SC2086
	as_root "${install_cmd[@]}" $pkgs
}

container_install() {
	local want="$1" self
	ensure_distrobox
	if ! box_exists "$BOX"; then
		say "Creating the '$BOX' container from $BOX_IMAGE"
		distrobox create --yes --name "$BOX" --image "$BOX_IMAGE"
	fi
	# The container shares this home directory, so a copy of this script put
	# here is visible inside it.
	self="$DATA/setup.sh"
	mkdir -p "$DATA"
	fetch_self "$self"
	local flags=(--native --yes)
	case "$want" in beta) flags+=(--beta) ;; minimal) flags+=(--minimal) ;; esac
	say "Building Atlas Monitor inside the container"
	distrobox enter "$BOX" -- env ATLAS_SETUP_LAUNCHER="distrobox-enter -n $BOX --" bash "$self" install "${flags[@]}"
	printf '%s\n' "$BOX" >"$DATA/container"
}

# fetch_self puts a copy of this script at $1: this file if there is one, the
# published one if it arrived through a pipe.
fetch_self() {
	if [ -f "${BASH_SOURCE[0]:-}" ]; then
		cp "${BASH_SOURCE[0]}" "$1"
	else
		curl -fsSL "https://raw.githubusercontent.com/EternalCoder454/atlas-monitor/main/setup.sh" -o "$1"
	fi
}

# --- Actions ---------------------------------------------------------------------

do_install() {
	local want="${channel:-main}"
	say "Installing Atlas Monitor ($want) on $(distro_name)"
	if pkg_owned; then
		die "Atlas Monitor is installed by your package manager ($(pkg_owned)); update or remove it that way."
	fi
	local rel=""
	if [ "$route" = "container" ] && ! in_box; then
		container_install "$want"
	elif [ "$route" = "auto" ] && ! in_box && rel="$(known_old)"; then
		# Asked before anything is installed on the host, which would only
		# be found too old afterwards.
		say "$rel ships GTK and libadwaita older than Atlas needs (GTK $NEED_GTK, libadwaita $NEED_ADW)."
		note "Atlas can still run here, built in a small Fedora container that shares"
		note "your home, processes and desktop (about 400 MB, through distrobox)."
		ask "Use a container?" || die "nothing installed"
		container_install "$want"
	elif detect_pm && [ -z "$pm" ] && [ "$route" = "auto" ] && { have distrobox || have podman || have docker; }; then
		# An image-based system — Silverblue, Kinoite, Bazzite, SteamOS —
		# installs nothing on the host this way, but brings containers.
		say "$(distro_name) has no package manager this script can install with, but it can run containers."
		ask "Build Atlas in a container?" || die "nothing installed"
		container_install "$want"
	else
		detect_pm
		libs_too_old=0
		native_install "$want"
		if [ "$libs_too_old" = 1 ]; then
			local old
			old="$(too_old)"
			say "$(distro_name) ships:"
			printf '%s\n' "$old" | sed 's/^/  - /'
			[ "$route" = "native" ] && die "too old to build Atlas here, and --native rules out the container."
			note "Atlas can still run here, built in a small Fedora container that shares"
			note "your home, processes and desktop (about 400 MB, through distrobox)."
			ask "Use a container?" || die "nothing installed"
			container_install "$want"
		fi
	fi
	# Inside the container the outer run reports; saying it twice is noise.
	in_box && return 0
	echo
	say "Atlas Monitor is installed."
	note "Open it from your app menu: search for \"Atlas\"."
	note "Update: bash $DATA/setup.sh update   (or the Update button in Settings)"
	note "Remove: bash $DATA/setup.sh uninstall"
	[ -f "$DATA/setup.sh" ] || fetch_self "$DATA/setup.sh" 2>/dev/null || true
}

do_update() {
	if box_installed && ! in_box; then
		ensure_distrobox
		local flags=(--yes)
		[ -n "$channel" ] && flags+=("--$([ "$channel" = main ] && echo release || echo "$channel")")
		# The newest script, so an update can bring its own fixes; the copy
		# already here if GitHub cannot be reached.
		if curl -fsSL "https://raw.githubusercontent.com/EternalCoder454/atlas-monitor/main/setup.sh" -o "$DATA/setup.sh.new"; then
			mv "$DATA/setup.sh.new" "$DATA/setup.sh"
		else
			rm -f "$DATA/setup.sh.new"
		fi
		local box
		box="$(cat "$DATA/container")"
		distrobox enter "$box" -- env ATLAS_SETUP_LAUNCHER="distrobox-enter -n $box --" bash "$DATA/setup.sh" update "${flags[@]}"
		return
	fi
	[ -d "$SRC/.git" ] || die "no installed copy to update under $SRC (install it with this script first)"
	local want="${channel:-$(installed_channel)}"
	say "Updating Atlas Monitor ($want)"
	detect_pm
	build_and_install "$want" "${ATLAS_SETUP_LAUNCHER:-}"
	say "Up to date. Restart Atlas Monitor to use the new version."
}

# pkg_owned names the package manager that owns an installed atlas-monitor.
pkg_owned() {
	local f
	for f in /usr/bin/atlas-monitor /usr/local/bin/atlas-monitor; do
		[ -e "$f" ] || continue
		if have pacman && pacman -Qo "$f" >/dev/null 2>&1; then echo "pacman: sudo pacman -R atlas-monitor"; return 0; fi
		if have dpkg && dpkg -S "$f" >/dev/null 2>&1; then echo "apt: sudo apt remove atlas-monitor"; return 0; fi
		if have rpm && rpm -qf "$f" >/dev/null 2>&1; then echo "rpm: sudo dnf remove atlas-monitor"; return 0; fi
	done
	return 1
}

do_uninstall() {
	# The source and this script's own copy are about to go; a shell standing
	# in either would be left in a directory that no longer exists.
	cd "$HOME" || cd /
	if pgrep -x atlas-monitor >/dev/null 2>&1; then
		die "Atlas Monitor is running; close it first (it puts back any app Energy Saver eased off as it closes)."
	fi
	say "Removing Atlas Monitor"
	if box_installed && ! in_box; then
		local box
		box="$(cat "$DATA/container")"
		if have distrobox && box_exists "$box"; then
			ask "Remove the '$box' container too?" && distrobox rm --force "$box"
		fi
	fi
	# The paths make install and the release tarball's install.sh both use.
	# Not `make uninstall`: it deletes the checkout it is running in.
	rm -f "$PREFIX/bin/atlas-monitor" \
		"$PREFIX/share/applications/com.atlas.Monitor.desktop" \
		"$PREFIX/share/icons/hicolor/scalable/apps/com.atlas.Monitor.svg" \
		"$PREFIX/share/icons/hicolor/16x16/apps/com.atlas.Monitor.svg" \
		"$PREFIX/share/icons/hicolor/symbolic/apps/com.atlas.Monitor-symbolic.svg" \
		"$PREFIX"/share/icons/hicolor/scalable/actions/atlas-*-symbolic.svg
	rm -rf "$DATA"
	have update-desktop-database && update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true
	have gtk4-update-icon-cache && gtk4-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
	if [ "$purge" = 1 ]; then
		rm -rf "${XDG_CONFIG_HOME:-$HOME/.config}/atlas-monitor" \
			"${XDG_STATE_HOME:-$HOME/.local/state}/atlas-monitor" \
			"${XDG_CACHE_HOME:-$HOME/.cache}/atlas-monitor"
		note "Settings, logs and caches removed."
	else
		note "Your settings are kept in ${XDG_CONFIG_HOME:-$HOME/.config}/atlas-monitor (--purge removes them)."
	fi
	if owner="$(pkg_owned)"; then
		note "A copy installed by your package manager is still there — ${owner#*: }"
	fi
	say "Atlas Monitor is removed."
}

do_check() {
	detect_pm
	say "$(distro_name)"
	note "package manager: ${pm:-unknown}"
	note "Go:          $(go env GOVERSION 2>/dev/null || echo missing)"
	note "GTK 4:       $(pkg-config --modversion gtk4 2>/dev/null || echo 'no development files')"
	note "libadwaita:  $(pkg-config --modversion libadwaita-1 2>/dev/null || echo 'no development files')"
	local missing old
	missing="$(missing_native)"
	old="$(too_old)"
	if [ -n "$old" ]; then
		say "Too old to build Atlas natively:"
		printf '%s\n' "$old" | sed 's/^/  - /'
		note "setup.sh would build it in a Fedora container instead."
	elif [ -n "$missing" ]; then
		say "Would install first:"
		printf '%s\n' "$missing" | sed 's/^/  - /'
		note "${install_cmd[*]:-(no known package manager)} $packages"
	else
		say "Ready to build Atlas natively."
	fi
	if box_installed; then note "installed in the '$(cat "$DATA/container")' container"; fi
	if [ -d "$SRC/.git" ]; then note "installed from $SRC ($(installed_channel))"; fi
}

case "$action" in
install) do_install ;;
update) do_update ;;
uninstall) do_uninstall ;;
check) do_check ;;
esac
