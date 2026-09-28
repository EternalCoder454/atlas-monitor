package ease

import "testing"

// pwDump mirrors pw-dump's output for three running streams, with the
// properties PipeWire really sets (taken from a Plasma 6 session):
//
//   - a native PipeWire client (pw-cat), traceable by the pid the server
//     took from its socket;
//   - a PulseAudio client (paplay) through pipewire-pulse, whose client
//     object carries pipewire-pulse's own pid, so only the pid it reported
//     for itself — checked against the binary it named — can be used;
//   - a Flatpak application, which PipeWire marks with its app ID;
//
// and one stream that is not running, which must be ignored.
const pwDump = `[
 {"id":160,"type":"PipeWire:Interface:Client","info":{"props":{"application.name":"pw-cat","application.process.binary":"pw-cat","pipewire.sec.pid":680429}}},
 {"id":201,"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"application.name":"pw-cat","client.id":160,"media.class":"Stream/Output/Audio"}}},
 {"id":150,"type":"PipeWire:Interface:Client","info":{"props":{"application.name":"paplay","application.process.binary":"pacat","client.api":"pipewire-pulse","pipewire.sec.pid":2927}}},
 {"id":202,"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"application.name":"paplay","application.process.binary":"pacat","application.process.id":680430,"client.api":"pipewire-pulse","client.id":150,"media.class":"Stream/Output/Audio"}}},
 {"id":120,"type":"PipeWire:Interface:Client","info":{"props":{"pipewire.access":"flatpak","pipewire.access.portal.app_id":"com.discordapp.Discord","pipewire.sec.pid":3}}},
 {"id":203,"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"client.id":120,"media.class":"Stream/Input/Audio"}}},
 {"id":204,"type":"PipeWire:Interface:Node","info":{"state":"suspended","props":{"application.process.id":"555","media.class":"Stream/Output/Audio"}}},
 {"id":205,"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"media.class":"Audio/Sink","node.name":"speakers"}}}
]`

func TestStreamsFromPwDump(t *testing.T) {
	got, err := streamsFromPwDump([]byte(pwDump))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("streams = %+v; want the three running ones, not the suspended one or the sink", got)
	}
	byBinary := map[string]stream{}
	for _, s := range got {
		byBinary[s.binary] = s
	}
	if s := byBinary["pw-cat"]; s.trusted != 680429 {
		t.Errorf("native client = %+v; want its socket pid trusted", s)
	}
	if s := byBinary["pacat"]; s.trusted != 0 || s.claimed != 680430 {
		t.Errorf("pulse client = %+v; pipewire-pulse's own pid must not be trusted as the app's", s)
	}
	if s := byBinary[""]; s.appID != "com.discordapp.Discord" {
		t.Errorf("flatpak stream = %+v; want its app ID", s)
	}
}

type fakeProcs struct {
	units map[int]string
	exes  map[int]string
}

func (f fakeProcs) unit(pid int) string    { return f.units[pid] }
func (f fakeProcs) exeName(pid int) string { return f.exes[pid] }
func (f fakeProcs) all() map[int]string    { return f.units }

func TestAppsOf(t *testing.T) {
	p := fakeProcs{
		units: map[int]string{
			100: "app-org.mozilla.firefox@1.service",
			200: "app-org.kde.elisa@2.service",
			300: "app-io.mpv.Mpv-3.scope",
			400: "app-org.kde.konsole@4.service",
			7:   "app-org.gnome.Nautilus@5.service",
		},
		exes: map[int]string{100: "firefox", 200: "elisa", 300: "mpv", 400: "mpv", 7: "nautilus"},
	}
	got := appsOf([]stream{
		{trusted: 100},                    // native, trusted
		{claimed: 200, binary: "elisa"},   // pulse, and the pid really is elisa
		{claimed: 7, binary: "mpv"},       // pulse from a sandbox: 7 is not mpv out here
		{appID: "com.discordapp.Discord"}, // flatpak
	}, p)
	want := map[string]bool{
		"org.mozilla.firefox":    true,
		"org.kde.elisa":          true,
		"io.mpv.Mpv":             true, // every app running an mpv...
		"org.kde.konsole":        true, // ...including the terminal one was started in
		"com.discordapp.Discord": true,
	}
	for id := range want {
		if !got[id] {
			t.Errorf("%s not marked audible", id)
		}
	}
	if got["org.gnome.Nautilus"] {
		t.Error("the process that merely has the sandbox pid's number was marked audible")
	}
}
