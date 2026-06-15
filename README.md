# Atlas Monitor

A native Linux system monitor for GNOME / Fedora, written in Go with GTK4 and
libadwaita. It is a lighter-weight alternative to Mission Center with a fixed
two-pane layout (the sidebar never overlaps the content) and first-class AMD GPU
support read straight from sysfs.

## Quick install

Fedora (one line — installs build deps, then clones, builds and installs to
`~/.local`):

```sh
sudo dnf install -y golang gtk4-devel libadwaita-devel glib2-devel gcc pkgconf-pkg-config git && \
  git clone https://github.com/EternalCoder454/atlas-monitor.git && \
  cd atlas-monitor && make install
```

Then press **Super** and search "Atlas". The first build compiles the gotk4 cgo
bindings and can take a few minutes; rebuilds are cached and fast.

## Features

- **Fixed two-pane layout** — a 200px sidebar that is always visible beside the
  content area, at any window size.
- **CPU** — live utilisation graph, per-core usage bars, frequencies, cache
  sizes, socket/core counts, and temperature (`coretemp`/`k10temp`).
- **Memory** — RAM and swap history, a Used/Cached/Free breakdown bar, and the
  full `/proc/meminfo`-derived figures.
- **Disk** (per device) — listed by hardware model (e.g. *Samsung SSD 970 EVO
  Plus 1TB*); read/write throughput graphs plus capacity and totals. The zram
  device is shown as **Swap** with a one-line explanation of what it is.
- **Network** (per interface) — download/upload graphs, addresses, MAC, and link
  speed.
- **GPU** — AMD (RDNA) utilisation and VRAM graphs, clocks, temperature, fan,
  power, and GTT, all from sysfs (no ROCm, no nvtop).
- **Apps** — a virtualised process table (`GtkColumnView`) with click-to-sort
  columns, live search, a right-click menu (End Task / Kill / Stop / Continue /
  Open file location), and an optional *Group by app* toggle.
- **Services** — systemd units over D-Bus with status dots and
  Start/Stop/Restart/Enable/Disable actions (polkit-authenticated).
- **Assistant** — an optional local AI (via [Ollama](https://ollama.com)) that
  answers questions about your machine — specs, the top CPU/memory processes,
  failed services — from a live system snapshot. Toggle it off any time in
  **Settings** (the gear icon).

## Performance

- Idle RSS target under 70 MB; one collector goroutine per subsystem on a 1s
  ticker, all reads off the GTK main thread.
- Graphs use fixed 60-sample ring buffers, pre-allocated at startup.
- Collection **pauses entirely while the window is hidden/minimised** (0% CPU),
  resuming when it is shown again.
- The expensive per-process scan only runs while the Apps view is open.

## Build dependencies

Fedora 40+/44:

```sh
sudo dnf install golang gtk4-devel libadwaita-devel glib2-devel gcc pkgconf-pkg-config
```

You need Go 1.22 or newer. The first build compiles the gotk4 cgo bindings and
can take several minutes; subsequent builds are cached and fast.

## Build & install

```sh
make            # build ./bin/atlas-monitor
make run        # build and run
make install    # install to ~/.local/bin and the stylesheet to
                # ~/.local/share/atlas-monitor/
make clean
```

`make install` honours `PREFIX` (default `~/.local`).

## Setting up the assistant

The **Assistant** view is optional and runs a model locally through
[Ollama](https://ollama.com) — nothing leaves your machine. The easiest way to
set it up is one command from the source folder:

```sh
make setup-ai
```

That installs Ollama (via its official installer — it prompts first if Ollama
isn't already present), starts the local server, and pulls the default model
(`qwen2.5:3b`, ~1.9 GB). It is safe to re-run and only does what is missing.

Prefer to do it by hand? Install Ollama, then pull the model:

```sh
curl -fsSL https://ollama.com/install.sh | sh
ollama pull qwen2.5:3b
```

If you open the Assistant before this is done, Atlas shows an in-app panel with
the exact commands (and a **Copy** button) and clears it automatically the
moment Ollama is ready — no need to restart. To use a different model, set it in
**Settings** (the gear) and run `make setup-ai <model>` (or `ollama pull
<model>`); GPU acceleration is detected automatically by Ollama's installer. You
can turn the assistant off entirely in Settings, which hides the view and stops
all AI activity.

## Notes

- **AMD GPU**: stats are read from `/sys/class/drm/card*/` and the `amdgpu`
  hwmon node. The card is auto-detected by PCI vendor ID `0x1002`, so it does not
  matter whether it is `card0`, `card1`, etc. No ROCm is required for monitoring.
- **Per-process network** is an estimate — exact per-process byte counts are not
  exposed by the kernel without elevated privileges, so interface throughput is
  attributed across processes by their open-socket count. These columns are
  labelled **Net ≈** to make the approximation explicit.
- **Per-process GPU** is read from the DRM fdinfo interface
  (`/proc/<pid>/fdinfo`, the `drm-engine-*` nanosecond counters), so each
  process's GPU engine load is shown — no root or debugfs required. Known GPU
  clients are sampled every tick with a periodic full rescan to find new ones.
- **Services**: Start/Stop/Restart/Enable/Disable are performed through systemd
  over the system bus, which triggers your desktop's polkit agent for
  authentication. Without authorisation the action returns an error shown in the
  view.
- **AI assistant**: talks to a local [Ollama](https://ollama.com) server
  (default `http://localhost:11434`, model `qwen2.5:3b`) — see [Setting up the
  assistant](#setting-up-the-assistant) for the one-command install. Each
  question sends a compact live snapshot — specs, top processes, services — as
  the system prompt; the model runs entirely on your machine. Configure the
  model/endpoint or turn it off completely via the gear → **Settings**. With AI
  disabled, no network calls are made and the Assistant entry is hidden.
  Settings persist to `~/.config/atlas-monitor/settings.json`.

## Development

- `make vet` runs `go vet ./...`.
- The non-GUI layers have integration tests that read this machine's live
  `/proc`, `/sys`, and D-Bus:
  `go test ./internal/stats/ ./internal/process/ ./internal/services/ -v`.
- `ATLAS_VIEW=<name>` opens a specific view at startup (e.g. `apps`,
  `services`, `gpu`, `memory`, `disk:nvme0n1`, `net:wlp7s0`) — handy for testing.

## Versioning

Atlas Monitor follows [Semantic Versioning](https://semver.org):
`MAJOR.MINOR.PATCH`.

- **MAJOR** — large or breaking changes.
- **MINOR** — new, backward-compatible features.
- **PATCH** — backward-compatible bugfixes.

`0.x` releases are pre-1.0 (the app is still evolving); `1.0.0` will mark the
first release declared stable. The current version lives in [`VERSION`](VERSION)
and is shown in **Settings → Application → Version**.

Tagged releases land on `main` (e.g. `v0.1.0`). Day-to-day development happens on
the `beta` branch (versioned `X.Y.Z-beta`); once a beta is approved it is merged
to `main`, the `-beta` suffix dropped, and the release tagged.

## Project layout

```
main.go                embeds the stylesheet, starts the app
internal/app/          AdwApplication, window, two-pane wiring
internal/ui/           sidebar, content stack, the individual views
internal/stats/        /proc + /sys collectors, ring buffers, pause/resume
internal/process/      per-process /proc/[pid] collection
internal/gpu/          AMD GPU sysfs reader (auto-detect)
internal/services/     systemd D-Bus client
internal/ai/           streaming Ollama client
internal/config/       persisted user settings (~/.config/atlas-monitor)
internal/graph/        reusable Cairo graph widget
internal/format/       byte/rate/clock formatting helpers
assets/style.css       theme-aware styling
```
