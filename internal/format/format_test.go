package format

import "testing"

func TestAppenders(t *testing.T) {
	const gib = 1024 * 1024 * 1024
	tests := []struct {
		got, want string
	}{
		{string(AppendBytes(nil, 0)), "0 B"},
		{string(AppendBytes(nil, 999)), "999 B"},
		{string(AppendBytes(nil, 2048)), "2 KiB"},
		{string(AppendBytes(nil, 5*1024*1024)), "5.0 MiB"},
		{string(AppendBytes(nil, 11*gib)), "11.00 GiB"},
		{string(AppendBytes(nil, 3*1024*gib)), "3.00 TiB"},
		{string(AppendGiB(nil, 11*gib/2)), "5.50 GiB"},
		{string(AppendRate(nil, 0)), "0 B/s"},
		{string(AppendRate(nil, 1536)), "2 KiB/s"},
		{string(AppendRate(nil, 1.5*1024*1024)), "1.5 MiB/s"},
		{string(AppendMHz(nil, 0)), "—"},
		{string(AppendMHz(nil, 800)), "800 MHz"},
		{string(AppendMHz(nil, 3456)), "3.46 GHz"},
		{string(AppendPercent(nil, 42.4)), "42%"},
		{string(AppendPercent1(nil, 42.44)), "42.4%"},
		{string(AppendTemp(nil, -1)), "—"},
		{string(AppendTemp(nil, 61.6)), "62 °C"},
		{string(AppendInt(nil, 32)), "32"},
		// The string wrappers must agree with the appenders.
		{Bytes(2048), "2 KiB"},
		{GiB(gib), "1.00 GiB"},
		{Rate(2048), "2 KiB/s"},
		{MHz(3456), "3.46 GHz"},
	}
	for i, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("case %d: got %q, want %q", i, tt.got, tt.want)
		}
	}
}

// TestAppendersDoNotAllocate is the point of the Append* API: the live views
// call these on hundreds of labels every second, into a buffer they already own.
func TestAppendersDoNotAllocate(t *testing.T) {
	buf := make([]byte, 0, 64)
	got := testing.AllocsPerRun(200, func() {
		buf = AppendBytes(buf[:0], 1234567)
		buf = AppendGiB(buf[:0], 1234567890)
		buf = AppendRate(buf[:0], 987654.5)
		buf = AppendMHz(buf[:0], 3456)
		buf = AppendPercent(buf[:0], 42.4)
		buf = AppendPercent1(buf[:0], 42.44)
		buf = AppendTemp(buf[:0], 61.6)
		buf = AppendInt(buf[:0], 32)
	})
	if got != 0 {
		t.Errorf("formatting allocated %v times per run, want 0", got)
	}
}
