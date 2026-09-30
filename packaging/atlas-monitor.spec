# RPM spec for Atlas Monitor. Suitable for COPR: the build needs network access
# for the Go module cache, so enable it on the COPR project
# (`copr-cli modify --enable-net on`) or run rpmbuild with modules pre-fetched.

# No debuginfo subpackage. The binary is linked with -s -w, which leaves no debug
# information to split out, and Fedora's rpmbuild then fails the whole build on
# an empty debugsourcefiles.list rather than skipping the subpackage.
%global debug_package %{nil}

Name:           atlas-monitor
Version:        %{?_version}%{!?_version:0.14.0}
Release:        1%{?dist}
Summary:        Lightweight system monitor for GNOME — CPU, memory, disk, network, GPU

License:        MIT
URL:            https://github.com/EternalCoder454/atlas-monitor
Source0:        %{url}/archive/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.24
BuildRequires:  gcc
BuildRequires:  pkgconfig(gtk4) >= 4.22
BuildRequires:  pkgconfig(libadwaita-1) >= 1.9
BuildRequires:  pkgconfig(glib-2.0)
BuildRequires:  pkgconfig(gobject-introspection-1.0)
BuildRequires:  desktop-file-utils

Requires:       gtk4 >= 4.22
Requires:       libadwaita >= 1.9
# hwdata supplies /usr/share/hwdata/pci.ids, which names the detected GPU.
Recommends:     hwdata
# The optional assistant talks to a local Ollama server; it is not packaged here.
Suggests:       ollama

%description
A native system monitor for GNOME and Fedora written in Go with GTK4 and
libadwaita. Live CPU, memory, disk, network and GPU graphs, a sortable process
table with per-process CPU, memory, GPU, network and disk figures, and systemd
service control over D-Bus.

GPU statistics are read directly from the kernel: amdgpu through sysfs, NVIDIA
through NVML when the proprietary driver is installed, and everything else
through the generic DRM interfaces.

%prep
%autosetup -n %{name}-%{version}

%build
export CGO_ENABLED=1
# Position-independent, as Fedora's packaging guidelines expect of every
# executable, so address-space randomisation applies to it; Go's default on
# linux/amd64 is a fixed-address binary, which rpmlint flags.
go build -buildmode=pie -trimpath -ldflags="-s -w" -o %{name} .

%install
install -Dm755 %{name}                       %{buildroot}%{_bindir}/%{name}
install -Dm644 assets/style.css              %{buildroot}%{_datadir}/%{name}/style.css
install -Dm644 assets/icon.svg               %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/com.atlas.Monitor.svg
for icon in cpu memory disk gpu assistant network wifi battery apps services settings prompts update trash reset menu warning startup energy sensors theme opacity text timer branch folder document; do
    install -Dm644 assets/icons/atlas-$icon-symbolic.svg \
        %{buildroot}%{_datadir}/icons/hicolor/scalable/actions/atlas-$icon-symbolic.svg
done
install -d %{buildroot}%{_datadir}/applications
sed 's|@BIN@|%{_bindir}/%{name}|g' assets/com.atlas.Monitor.desktop \
    > %{buildroot}%{_datadir}/applications/com.atlas.Monitor.desktop

%check
desktop-file-validate %{buildroot}%{_datadir}/applications/com.atlas.Monitor.desktop

%files
%license LICENSE NOTICE
%doc README.md
%{_bindir}/%{name}
%{_datadir}/%{name}/
%{_datadir}/applications/com.atlas.Monitor.desktop
%{_datadir}/icons/hicolor/scalable/apps/com.atlas.Monitor.svg
%{_datadir}/icons/hicolor/scalable/actions/atlas-*-symbolic.svg

%changelog
* Wed Sep 30 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.14.0-1
- setup.sh installs, updates and removes Atlas on any distribution
- Release, Beta and Minimal are channels chosen in Settings
- Solid sidebar option under transparency; flat cards; less memory and work

* Wed Sep 30 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.13.0-1
- Settings is a single page in the window, laid out like Task Manager's
- Optional window transparency, and ruled tables sized to each theme
- Much less work to draw each second, most of all on Settings and Apps

* Tue Sep 29 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.12.0-1
- A new look modelled on the Windows 11 Task Manager
- The Apps list shades busy programs by how busy they are
- A Sensors page with every temperature, fan, voltage and power reading
- Group by app groups by application, and every program has a Details window
- Energy Saver can ease busy apps off by itself, never one playing sound
- Closed windows and dialogs are freed, so memory stays flat over long sessions

* Mon Sep 28 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.11.2-1
- Runs on Windows, from a zip that unpacks anywhere
- Ten colour themes, each held to readable contrast for text and buttons
- Settings shows one page at a time in a narrow window instead of clipping
- Opening on the Apps page no longer keeps the processor busy for ten seconds
- The Apps and Services lists are cheaper to draw and to refresh
- Changing a setting no longer restyles the whole window or pauses the readings

* Sat Sep 26 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.11.1-1
- Updating works however Atlas was installed, not only from a source checkout
- A packaged copy is told which command updates it rather than being told to run make install
- New versions are found without a source checkout to compare against
- An update installs back over the running copy instead of leaving a second one behind
- Missing build dependencies are named along with the command that installs them

* Thu Sep 24 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.8.3-1
- The Apps list reorders live so the busiest program stays at the top
- Heavy processor users are highlighted in the list
- A new icon set throughout, drawn from Material Symbols
- The sidebar highlights the page that is actually open
- Speed graphs show their highest recent reading

* Thu Sep 24 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.8.2-1
- Atlas now tells you when an update is available, with a list of what changed
- A new Settings switch turns that check off
- The Apps page no longer grows in memory as programs start and stop

* Thu Sep 24 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.8.1-1
- End Task can no longer signal a process that merely reused the PID
- The updater no longer treats its own checkout path as shell syntax
- Settings warns when the assistant would send this machine's details off it
- Logs moved out of shared /tmp paths; the settings file is no longer world readable

* Thu Sep 24 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.8.0-1
- Fix a memory leak on the Apps page that grew about a megabyte a minute
- Launching Atlas twice no longer builds a second app inside the first
- Text now rasterises the way the desktop asks, fixing soft text at 1080p
- Per-core readings, a capacity bar on Storage, live figures in the sidebar
- Idle readings in the process table are dimmed so activity stands out

* Thu Sep 24 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.7.0-1
- Halve the CPU and cut read syscalls by two thirds in the collectors

* Wed Sep 23 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.6.1-1
- New CPU, memory, disk and GPU icons
