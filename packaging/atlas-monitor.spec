# RPM spec for Atlas Monitor. Suitable for COPR: the build needs network access
# for the Go module cache, so enable it on the COPR project
# (`copr-cli modify --enable-net on`) or run rpmbuild with modules pre-fetched.
Name:           atlas-monitor
Version:        %{?_version}%{!?_version:0.8.2}
Release:        1%{?dist}
Summary:        Lightweight system monitor for GNOME — CPU, memory, disk, network, GPU

License:        MIT
URL:            https://github.com/EternalCoder454/atlas-monitor
Source0:        %{url}/archive/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.24
BuildRequires:  gcc
BuildRequires:  pkgconfig(gtk4)
BuildRequires:  pkgconfig(libadwaita-1) >= 1.6
BuildRequires:  pkgconfig(glib-2.0)
BuildRequires:  desktop-file-utils

Requires:       gtk4
Requires:       libadwaita >= 1.6
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
go build -trimpath -ldflags="-s -w" -o %{name} .

%install
install -Dm755 %{name}                       %{buildroot}%{_bindir}/%{name}
install -Dm644 assets/style.css              %{buildroot}%{_datadir}/%{name}/style.css
install -Dm644 assets/icon.svg               %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/com.atlas.Monitor.svg
for icon in cpu memory disk gpu assistant network wifi battery apps services settings prompts update trash reset; do
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

* Wed Sep 24 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.7.0-1
- Halve the CPU and cut read syscalls by two thirds in the collectors

* Tue Sep 23 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.6.1-1
- New CPU, memory, disk and GPU icons
