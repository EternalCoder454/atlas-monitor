package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/ai"
	"atlas-monitor/internal/config"
	"atlas-monitor/internal/format"
	"atlas-monitor/internal/process"
	"atlas-monitor/internal/services"
	"atlas-monitor/internal/stats"
)

type assistantView struct {
	root     *gtk.Box
	col      *stats.Collector
	proc     *process.Collector
	ai       *ai.Client
	settings *config.Settings
	svc      *services.Client

	titleLabel *gtk.Label
	caption    *gtk.Label
	transcript *gtk.TextView
	scroller   *gtk.ScrolledWindow
	entry      *gtk.Entry
	send       *gtk.Button
	endMark    *gtk.TextMark // right gravity: scroll target at the very end
	respStart  *gtk.TextMark // left gravity: start of the in-flight response

	history     []ai.Message
	busy        bool
	streamStart time.Time
	streamTokens int
	lastStats   ai.Stats
}

func newAssistantView(col *stats.Collector, proc *process.Collector, client *ai.Client, settings *config.Settings) *assistantView {
	v := &assistantView{col: col, proc: proc, ai: client, settings: settings}
	v.svc, _ = services.NewClient()

	v.root = gtk.NewBox(gtk.OrientationVertical, 10)
	v.root.SetMarginTop(14)
	v.root.SetMarginBottom(14)
	v.root.SetMarginStart(14)
	v.root.SetMarginEnd(14)

	header := gtk.NewBox(gtk.OrientationVertical, 2)
	v.titleLabel = newTitle(settings.AssistantTitle)
	header.Append(v.titleLabel)
	v.caption = gtk.NewLabel("")
	v.caption.AddCSSClass("am-subtle")
	v.caption.SetXAlign(0)
	header.Append(v.caption)
	v.root.Append(header)

	v.transcript = gtk.NewTextView()
	v.transcript.SetEditable(false)
	v.transcript.SetCursorVisible(false)
	v.transcript.SetWrapMode(gtk.WrapWordChar)
	v.transcript.SetLeftMargin(8)
	v.transcript.SetRightMargin(8)
	v.transcript.SetTopMargin(8)
	v.transcript.SetBottomMargin(8)
	buf := v.transcript.Buffer()
	v.endMark = buf.CreateMark("end", buf.EndIter(), false)
	v.respStart = buf.CreateMark("resp", buf.EndIter(), true)

	v.scroller = gtk.NewScrolledWindow()
	v.scroller.SetChild(v.transcript)
	v.scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.scroller.SetVExpand(true)
	v.scroller.AddCSSClass("am-chat")
	v.root.Append(v.scroller)

	inputRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	v.entry = gtk.NewEntry()
	v.entry.SetHExpand(true)
	v.entry.SetPlaceholderText("Ask about your CPU, memory, processes, services…")
	v.entry.ConnectActivate(v.onSend)
	v.send = gtk.NewButtonWithLabel("Send")
	v.send.AddCSSClass("suggested-action")
	v.send.ConnectClicked(v.onSend)
	inputRow.Append(v.entry)
	inputRow.Append(v.send)
	v.root.Append(inputRow)

	v.appendPlain("Ask me about this computer — e.g. \"what's using the most memory?\", " +
		"\"any failed services?\", or \"what are my specs?\"\n\n")
	return v
}

func (v *assistantView) Root() gtk.Widgetter { return v.root }

func (v *assistantView) Update() {
	if !v.settings.AIEnabled {
		v.caption.SetText("AI disabled — enable it in Settings")
		v.entry.SetSensitive(false)
		v.send.SetSensitive(false)
		return
	}
	v.titleLabel.SetText(v.settings.AssistantTitle)
	v.refreshCaption()
	v.entry.SetSensitive(!v.busy)
	v.send.SetSensitive(!v.busy)
}

func (v *assistantView) refreshCaption() {
	model := v.ai.Model()
	switch {
	case v.busy && v.streamTokens > 0:
		rate := 0.0
		if el := time.Since(v.streamStart).Seconds(); el > 0 {
			rate = float64(v.streamTokens) / el
		}
		v.caption.SetText(fmt.Sprintf("%s  ·  generating… %d tok, %.0f tok/s", model, v.streamTokens, rate))
	case v.busy:
		v.caption.SetText(model + "  ·  thinking…")
	case v.lastStats.EvalCount > 0:
		s := v.lastStats
		v.caption.SetText(fmt.Sprintf("%s  ·  %.1f tok/s  ·  %d tokens  ·  %.1fs",
			model, s.TokensPerSec(), s.EvalCount, s.TotalDuration.Seconds()))
	default:
		v.caption.SetText("Local model: " + model)
	}
}

func (v *assistantView) onSend() {
	if v.busy || !v.settings.AIEnabled {
		return
	}
	q := strings.TrimSpace(v.entry.Text())
	if q == "" {
		return
	}
	v.entry.SetText("")
	v.appendMarkup("<b>You:</b> ")
	v.appendPlain(q + "\n\n")
	v.appendMarkup("<b>" + pangoEscape(v.settings.AssistantTitle) + ":</b> ")

	buf := v.transcript.Buffer()
	buf.MoveMark(v.respStart, buf.EndIter()) // response (raw stream) begins here

	v.history = append(v.history, ai.Message{Role: "user", Content: q})
	v.busy = true
	v.streamStart = time.Now()
	v.streamTokens = 0
	v.entry.SetSensitive(false)
	v.send.SetSensitive(false)
	v.refreshCaption()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		system := v.buildContext()
		msgs := append([]ai.Message{{Role: "system", Content: system}}, v.history...)
		full, stats, err := v.ai.Chat(ctx, msgs, func(tok string) {
			glib.IdleAdd(func() {
				v.streamTokens++
				v.appendPlain(tok)
				v.refreshCaption()
			})
		})
		glib.IdleAdd(func() { v.finish(full, stats, err) })
	}()
}

func (v *assistantView) finish(full string, stats ai.Stats, err error) {
	if err != nil {
		v.appendPlain("\n[error: " + err.Error() + "]\n\n")
	} else {
		// Replace the raw streamed text with its Markdown-rendered version.
		buf := v.transcript.Buffer()
		start := buf.IterAtMark(v.respStart)
		buf.Delete(start, buf.EndIter())
		buf.InsertMarkup(buf.EndIter(), markdownToPango(full))
		v.appendPlain("\n\n")
		v.history = append(v.history, ai.Message{Role: "assistant", Content: full})
		v.lastStats = stats
	}
	if len(v.history) > 8 { // keep the context window small for a 3B model
		v.history = v.history[len(v.history)-8:]
	}
	v.busy = false
	v.refreshCaption()
	v.entry.SetSensitive(true)
	v.send.SetSensitive(true)
	v.entry.GrabFocus()
}

func (v *assistantView) appendPlain(s string) {
	buf := v.transcript.Buffer()
	buf.Insert(buf.EndIter(), s)
	v.scrollToEnd()
}

func (v *assistantView) appendMarkup(m string) {
	buf := v.transcript.Buffer()
	buf.InsertMarkup(buf.EndIter(), m)
	v.scrollToEnd()
}

func (v *assistantView) scrollToEnd() {
	v.transcript.ScrollToMark(v.endMark, 0, false, 0, 0)
}

// buildContext assembles the live system snapshot sent as the system prompt.
// Runs off the main thread (called from the send goroutine).
func (v *assistantView) buildContext() string {
	var b strings.Builder
	b.WriteString(v.settings.SystemPrompt)
	b.WriteString("\n\n[MACHINE]\n")
	b.WriteString(systemFacts())

	b.WriteString("\n[HARDWARE]\n")
	v.col.Read(func(s *stats.Stats) {
		c := s.CPU
		fmt.Fprintf(&b, "CPU: %s, %d cores / %d threads, base %s, now %.0f%% @ %s",
			c.Model, c.PhysCores, c.Logical, format.MHz(c.BaseFreq), c.Usage, format.MHz(c.CurFreq))
		if c.Temp >= 0 {
			fmt.Fprintf(&b, ", %.0f°C", c.Temp)
		}
		b.WriteByte('\n')
		if c.L2 != "" || c.L3 != "" {
			fmt.Fprintf(&b, "CPU cache: L2 %s, L3 %s\n", orDash(c.L2), orDash(c.L3))
		}
		m := s.Mem
		fmt.Fprintf(&b, "Memory: %s used of %s (%s), %s cached, %s available; swap %s of %s\n",
			format.GiB(m.Used), format.GiB(m.Total), pctStr(m.Used, m.Total),
			format.GiB(m.Cached), format.GiB(m.Available), format.GiB(m.SwapUsed), format.GiB(m.SwapTotal))
		if s.GPU.Available {
			g := s.GPU
			fmt.Fprintf(&b, "GPU: %s, %.0f%% used, VRAM %s of %s, %.0f°C, %.0fW\n",
				g.Name, g.Usage, format.GiB(g.VramUsed), format.GiB(g.VramTotal), g.Temp, g.PowerW)
		}
		for _, d := range s.Disks {
			if d.IsSwap {
				fmt.Fprintf(&b, "Disk %s (%s): %s, compressed-RAM swap\n", d.Label(), d.Name, format.Bytes(d.SizeBytes))
			} else {
				fmt.Fprintf(&b, "Disk %s (%s): %s total, %s used, %s free\n",
					d.Label(), d.Name, format.Bytes(d.SizeBytes), format.Bytes(d.Used), format.Bytes(d.Free))
			}
		}
		for _, n := range s.Nets {
			if n.IPv4 == "" && n.RxRate == 0 && n.TxRate == 0 {
				continue // skip unconfigured/idle virtual interfaces
			}
			fmt.Fprintf(&b, "Network %s (%s): ", n.Label(), n.Name)
			if n.IPv4 != "" {
				fmt.Fprintf(&b, "%s, ", n.IPv4)
			}
			fmt.Fprintf(&b, "down %s, up %s\n", format.Rate(n.RxRate), format.Rate(n.TxRate))
		}
	})

	if snap := v.proc.Snapshot(); len(snap) > 0 {
		fmt.Fprintf(&b, "\n[PROCESSES] %d running\n", len(snap))
		b.WriteString("Top by CPU:\n")
		for _, p := range topProcs(snap, func(a, b process.Proc) bool { return a.CPU > b.CPU }, 6) {
			fmt.Fprintf(&b, "- %s (pid %d): %.0f%% CPU, %s\n", p.Name, p.PID, p.CPU, format.Bytes(p.RSS))
		}
		b.WriteString("Top by memory:\n")
		for _, p := range topProcs(snap, func(a, b process.Proc) bool { return a.RSS > b.RSS }, 6) {
			fmt.Fprintf(&b, "- %s (pid %d): %s, %.0f%% CPU\n", p.Name, p.PID, format.Bytes(p.RSS), p.CPU)
		}
		if gpu := topProcs(snap, func(a, b process.Proc) bool { return a.GPU > b.GPU }, 6); len(gpu) > 0 && gpu[0].GPU > 0 {
			b.WriteString("Top by GPU:\n")
			for _, p := range gpu {
				if p.GPU <= 0 {
					break
				}
				fmt.Fprintf(&b, "- %s (pid %d): %.0f%% GPU\n", p.Name, p.PID, p.GPU)
			}
		}
	} else {
		b.WriteString("\n(Process list is still warming up.)\n")
	}

	if v.svc != nil {
		if list, err := v.svc.List(true); err == nil {
			running := 0
			var failed []string
			for _, s := range list {
				switch s.Status {
				case services.Running:
					running++
				case services.Failed:
					failed = append(failed, s.Name)
				}
			}
			fmt.Fprintf(&b, "\n[SERVICES] %d total, %d running, %d failed\n", len(list), running, len(failed))
			if len(failed) > 0 {
				b.WriteString("Failed: " + strings.Join(failed, ", ") + "\n")
			}
		}
	}
	return b.String()
}

func topProcs(procs []process.Proc, less func(a, b process.Proc) bool, n int) []process.Proc {
	cp := make([]process.Proc, len(procs))
	copy(cp, procs)
	sort.Slice(cp, func(i, j int) bool { return less(cp[i], cp[j]) })
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp
}

func pctStr(used, total uint64) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", float64(used)/float64(total)*100)
}

// systemFacts gathers OS / kernel / host / uptime / load so the assistant knows
// the broader environment, not just the live metrics.
func systemFacts() string {
	var b strings.Builder
	if name := osReleaseName(); name != "" {
		fmt.Fprintf(&b, "OS: %s\n", name)
	}
	if k, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		fmt.Fprintf(&b, "Kernel: %s\n", strings.TrimSpace(string(k)))
	}
	if h, err := os.Hostname(); err == nil {
		fmt.Fprintf(&b, "Hostname: %s\n", h)
	}
	if u := os.Getenv("USER"); u != "" {
		fmt.Fprintf(&b, "User: %s\n", u)
	}
	if up := uptimeStr(); up != "" {
		fmt.Fprintf(&b, "Uptime: %s\n", up)
	}
	if la, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(la)); len(f) >= 3 {
			fmt.Fprintf(&b, "Load average: %s %s %s (1/5/15 min)\n", f[0], f[1], f[2])
		}
	}
	return b.String()
}

func osReleaseName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, "\"")
		}
	}
	return ""
}

func uptimeStr() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return ""
	}
	secs, _ := strconv.ParseFloat(f[0], 64)
	d := time.Duration(secs) * time.Second
	days, hours, mins := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
