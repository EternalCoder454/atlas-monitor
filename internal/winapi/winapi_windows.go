// Package winapi is the Windows system calls Atlas needs that
// golang.org/x/sys/windows does not already wrap.
//
// It exists so that the awkward part of the Windows port lives in one place.
// Everything here is either a LazyDLL binding or a struct whose layout has to
// match what the kernel writes, and both are the kind of thing that fails
// silently — a wrong offset does not crash, it reports plausible nonsense. The
// collectors above this package see ordinary Go types.
//
// This is developed without a Windows machine to run it on, so where a mistake
// could be quiet it is made loud instead: the structures the kernel fills carry
// compile-time size assertions against their documented sizes, so a layout error
// fails the build rather than becoming a wrong number on a chart.
package winapi

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	psapi    = windows.NewLazySystemDLL("psapi.dll")
	iphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")
	procGetPerformanceInfo   = psapi.NewProc("GetPerformanceInfo")
	procGetIfTable2          = iphlpapi.NewProc("GetIfTable2")
	procFreeMibTable         = iphlpapi.NewProc("FreeMibTable")
)

// hundredNS is the unit Windows reports every duration in: 100-nanosecond
// intervals. Dividing by it gives seconds.
const hundredNS = 1e7

// ---------------------------------------------------------------- memory

// memoryStatusEx is MEMORYSTATUSEX. Length must be set before the call; the
// kernel uses it to tell which version of the structure it was handed.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// performanceInformation is PERFORMANCE_INFORMATION. The page counts are in
// pages, not bytes — PageSize is in the same structure precisely because it
// varies.
type performanceInformation struct {
	Size              uint32
	CommitTotal       uintptr
	CommitLimit       uintptr
	CommitPeak        uintptr
	PhysicalTotal     uintptr
	PhysicalAvailable uintptr
	SystemCache       uintptr
	KernelTotal       uintptr
	KernelPaged       uintptr
	KernelNonpaged    uintptr
	PageSize          uintptr
	HandleCount       uint32
	ProcessCount      uint32
	ThreadCount       uint32
}

// Memory is what the machine's RAM and page file are doing, in bytes.
type Memory struct {
	TotalPhys uint64
	AvailPhys uint64
	// Cached is the system file cache. It is the nearest thing Windows has to
	// the Cached + SReclaimable + Buffers that Atlas shows on Linux.
	Cached uint64

	// PageFileTotal and PageFileUsed describe the page file, which is where
	// Atlas's "swap" comes from on Windows.
	//
	// Windows does not report the page file directly here. What it reports is
	// the commit charge: CommitLimit is physical memory plus the page file, and
	// CommitTotal is how much of that has been promised to processes. So the
	// page file's size is the part of the limit that is not RAM, and the part of
	// it in use is whatever has been committed beyond what is actually resident.
	// That is an estimate rather than a reading, and it is the same estimate
	// Task Manager's "Committed" line is built from.
	PageFileTotal uint64
	PageFileUsed  uint64
}

// ReadMemory samples memory and the page file.
func ReadMemory() (Memory, error) {
	var m Memory

	ms := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 {
		return m, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	m.TotalPhys, m.AvailPhys = ms.TotalPhys, ms.AvailPhys

	pi := performanceInformation{Size: uint32(unsafe.Sizeof(performanceInformation{}))}
	r, _, err = procGetPerformanceInfo.Call(uintptr(unsafe.Pointer(&pi)), uintptr(pi.Size))
	if r == 0 {
		// Memory itself was read; only the derived figures are missing, and a
		// caller that shows zero for the cache is better off than one that shows
		// nothing at all.
		return m, nil
	}
	page := uint64(pi.PageSize)
	m.Cached = uint64(pi.SystemCache) * page

	commitLimit := uint64(pi.CommitLimit) * page
	commitTotal := uint64(pi.CommitTotal) * page
	physTotal := uint64(pi.PhysicalTotal) * page
	physAvail := uint64(pi.PhysicalAvailable) * page

	if commitLimit > physTotal {
		m.PageFileTotal = commitLimit - physTotal
	}
	if resident := physTotal - physAvail; commitTotal > resident {
		used := commitTotal - resident
		if used > m.PageFileTotal {
			used = m.PageFileTotal
		}
		m.PageFileUsed = used
	}
	return m, nil
}

// ---------------------------------------------------------------- processor

// processorPerformance is SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION, one per
// logical processor.
type processorPerformance struct {
	IdleTime       int64
	KernelTime     int64
	UserTime       int64
	DpcTime        int64
	InterruptTime  int64
	InterruptCount uint32
	_              uint32 // padding to an 8-byte boundary
}

// SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION is 48 bytes on 64-bit Windows.
//
// A constant subtraction is the assertion: uintptr is unsigned, so if the sizes
// disagree one of the two goes negative and does not compile. Both directions are
// needed, because only one of them can overflow.
const (
	_ = unsafe.Sizeof(processorPerformance{}) - 48
	_ = 48 - unsafe.Sizeof(processorPerformance{})
)

// CPUTimes is one logical processor's cumulative time, in seconds.
//
// Busy excludes idle. Windows reports KernelTime with idle *included*, which is
// the single easiest thing to get wrong here — left uncorrected every core reads
// as permanently busy — so the subtraction happens once, on the way out.
type CPUTimes struct {
	Idle  float64
	Busy  float64
	Total float64
}

// ReadCPUTimes returns one entry per logical processor.
func ReadCPUTimes() ([]CPUTimes, error) {
	// The number of processors is not known in advance, so ask, then grow if the
	// answer changes underneath us (it can: cores are hot-pluggable on a VM).
	n := 64
	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]processorPerformance, n)
		size := uint32(len(buf)) * uint32(unsafe.Sizeof(processorPerformance{}))
		var got uint32
		err := windows.NtQuerySystemInformation(
			windows.SystemProcessorPerformanceInformation,
			unsafe.Pointer(&buf[0]), size, &got)
		if err != nil {
			if err == windows.STATUS_INFO_LENGTH_MISMATCH {
				n *= 2
				continue
			}
			return nil, fmt.Errorf("NtQuerySystemInformation(processor): %w", err)
		}
		count := int(got) / int(unsafe.Sizeof(processorPerformance{}))
		out := make([]CPUTimes, count)
		for i := 0; i < count; i++ {
			p := buf[i]
			idle := float64(p.IdleTime) / hundredNS
			// KernelTime includes IdleTime; user time is separate.
			kernel := float64(p.KernelTime)/hundredNS - idle
			user := float64(p.UserTime) / hundredNS
			out[i] = CPUTimes{Idle: idle, Busy: kernel + user, Total: idle + kernel + user}
		}
		return out, nil
	}
	return nil, fmt.Errorf("NtQuerySystemInformation(processor): processor count keeps changing")
}

// ---------------------------------------------------------------- processes

// Process is one running process, as the kernel describes it.
type Process struct {
	PID       int
	ParentPID int
	Name      string
	// CPUSeconds is cumulative user + kernel time. A rate needs two samples.
	CPUSeconds float64
	// WorkingSet is resident bytes; Private is the part not shared with another
	// process, which is the closer analogue of what Atlas shows on Linux.
	WorkingSet uint64
	Private    uint64
	// ReadBytes and WriteBytes are cumulative I/O, including file system cache
	// hits — the same caveat the Linux figures carry.
	ReadBytes  uint64
	WriteBytes uint64
	Threads    uint32
	SessionID  uint32
}

// ReadProcesses returns every process in one call.
//
// This is the whole process table, CPU times and I/O counters included, from a
// single system call — where Linux needs an open and a read per process. The
// buffer has to be sized by trial because processes come and go between asking
// how much room is needed and using it.
func ReadProcesses() ([]Process, error) {
	size := uint32(512 << 10)
	for attempt := 0; attempt < 6; attempt++ {
		buf := make([]byte, size)
		var got uint32
		err := windows.NtQuerySystemInformation(
			windows.SystemProcessInformation,
			unsafe.Pointer(&buf[0]), size, &got)
		if err != nil {
			if err == windows.STATUS_INFO_LENGTH_MISMATCH {
				// got is a hint, not a promise; leave room for arrivals.
				if got > size {
					size = got + (64 << 10)
				} else {
					size *= 2
				}
				continue
			}
			return nil, fmt.Errorf("NtQuerySystemInformation(process): %w", err)
		}
		return parseProcesses(buf[:got]), nil
	}
	return nil, fmt.Errorf("NtQuerySystemInformation(process): buffer never large enough")
}

// parseProcesses walks the linked list the kernel wrote into buf.
func parseProcesses(buf []byte) []Process {
	var out []Process
	const entrySize = unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{})

	for offset := 0; offset+int(entrySize) <= len(buf); {
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset]))

		proc := Process{
			PID:        int(p.UniqueProcessID),
			ParentPID:  int(p.InheritedFromUniqueProcessID),
			Name:       ntString(p.ImageName),
			CPUSeconds: float64(p.UserTime+p.KernelTime) / hundredNS,
			WorkingSet: uint64(p.WorkingSetSize),
			Private:    uint64(p.PrivatePageCount),
			ReadBytes:  uint64(p.ReadTransferCount),
			WriteBytes: uint64(p.WriteTransferCount),
			Threads:    p.NumberOfThreads,
			SessionID:  p.SessionID,
		}
		// The idle process has no image name and its "CPU time" is the machine
		// being idle, which is not a process anybody wants in a list.
		if proc.Name == "" && proc.PID == 0 {
			proc.Name = "System Idle Process"
		}
		out = append(out, proc)

		if p.NextEntryOffset == 0 {
			break
		}
		next := offset + int(p.NextEntryOffset)
		if next <= offset || next >= len(buf) {
			break // a malformed chain must not become an infinite loop
		}
		offset = next
	}
	return out
}

// ntString converts a counted UTF-16 string. Length is in bytes, not characters,
// which is the other easy mistake in this file.
func ntString(s windows.NTUnicodeString) string {
	if s.Buffer == nil || s.Length == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice(s.Buffer, s.Length/2))
}

// ---------------------------------------------------------------- power

// systemPowerStatus is SYSTEM_POWER_STATUS.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// Power is the battery and where the machine's power is coming from.
type Power struct {
	// OnAC is true when plugged in. Unknown is true when Windows will not say,
	// which is the answer on most desktops.
	OnAC    bool
	Unknown bool
	// HasBattery is false on a machine with none.
	HasBattery bool
	// Percent is 0..100, valid only when HasBattery.
	Percent int
	// SecondsLeft is the remaining runtime estimate, or -1 when there is none.
	SecondsLeft int64
	Charging    bool
}

// ReadPower reads the power status.
func ReadPower() (Power, error) {
	var s systemPowerStatus
	r, _, err := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return Power{}, fmt.Errorf("GetSystemPowerStatus: %w", err)
	}

	const (
		acOffline    = 0
		acOnline     = 1
		acUnknown    = 255
		flagCharging = 8
		flagNone     = 128
		flagUnknown  = 255
		pctUnknown   = 255
		timeUnknown  = 0xFFFFFFFF
	)

	p := Power{
		OnAC:        s.ACLineStatus == acOnline,
		Unknown:     s.ACLineStatus == acUnknown,
		HasBattery:  s.BatteryFlag != flagNone && s.BatteryFlag != flagUnknown,
		Charging:    s.BatteryFlag&flagCharging != 0,
		SecondsLeft: -1,
	}
	if p.HasBattery && s.BatteryLifePercent != pctUnknown {
		p.Percent = int(s.BatteryLifePercent)
	}
	if s.BatteryLifeTime != timeUnknown {
		p.SecondsLeft = int64(s.BatteryLifeTime)
	}
	return p, nil
}

// ---------------------------------------------------------------- network

// mibIfRow2 is MIB_IF_ROW2. Every field is present because the layout has to
// match, not because Atlas reads them all.
//
// The 64-bit counters are the reason this is used instead of the older
// GetIfTable: MIB_IFROW counts octets in 32 bits, which wraps every four
// gigabytes and would make a network graph lie on any busy machine.
type mibIfRow2 struct {
	InterfaceLuid  uint64
	InterfaceIndex uint32
	InterfaceGuid  windows.GUID

	Alias       [257]uint16
	Description [257]uint16

	PhysicalAddressLength    uint32
	PhysicalAddress          [32]uint8
	PermanentPhysicalAddress [32]uint8

	Mtu                uint32
	Type               uint32
	TunnelType         uint32
	MediaType          uint32
	PhysicalMediumType uint32
	AccessType         uint32
	DirectionType      uint32

	// A byte of bit-fields in C, and a byte here.
	InterfaceAndOperStatusFlags uint8

	OperStatus        uint32
	AdminStatus       uint32
	MediaConnectState uint32
	NetworkGuid       windows.GUID
	ConnectionType    uint32

	TransmitLinkSpeed uint64
	ReceiveLinkSpeed  uint64

	InOctets          uint64
	InUcastPkts       uint64
	InNUcastPkts      uint64
	InDiscards        uint64
	InErrors          uint64
	InUnknownProtos   uint64
	InUcastOctets     uint64
	InMulticastOctets uint64
	InBroadcastOctets uint64

	OutOctets          uint64
	OutUcastPkts       uint64
	OutNUcastPkts      uint64
	OutDiscards        uint64
	OutErrors          uint64
	OutUcastOctets     uint64
	OutMulticastOctets uint64
	OutBroadcastOctets uint64
	OutQLen            uint64
}

// MIB_IF_ROW2 is documented as 1352 bytes on 64-bit Windows, with the byte
// counters at these offsets. Get any of it wrong and the counters read as
// plausible nonsense rather than failing, so it is checked at build time.
//
// The total size alone is not enough, and neither are the counters on their own.
// Inserting a spare uint32 early in this structure changes neither: the four
// bytes come out of the padding that otherwise sits before TransmitLinkSpeed, so
// the size is identical and every field from there on keeps its offset — while
// Alias, Description, Type and OperStatus all quietly shift. Every field this
// package actually dereferences therefore has its offset pinned.
const (
	_ = unsafe.Sizeof(mibIfRow2{}) - 1352
	_ = 1352 - unsafe.Sizeof(mibIfRow2{})

	_ = unsafe.Offsetof(mibIfRow2{}.InterfaceIndex) - 8
	_ = 8 - unsafe.Offsetof(mibIfRow2{}.InterfaceIndex)

	_ = unsafe.Offsetof(mibIfRow2{}.Alias) - 28
	_ = 28 - unsafe.Offsetof(mibIfRow2{}.Alias)

	_ = unsafe.Offsetof(mibIfRow2{}.Description) - 542
	_ = 542 - unsafe.Offsetof(mibIfRow2{}.Description)

	_ = unsafe.Offsetof(mibIfRow2{}.Type) - 1128
	_ = 1128 - unsafe.Offsetof(mibIfRow2{}.Type)

	_ = unsafe.Offsetof(mibIfRow2{}.OperStatus) - 1156
	_ = 1156 - unsafe.Offsetof(mibIfRow2{}.OperStatus)

	_ = unsafe.Offsetof(mibIfRow2{}.ReceiveLinkSpeed) - 1200
	_ = 1200 - unsafe.Offsetof(mibIfRow2{}.ReceiveLinkSpeed)

	_ = unsafe.Offsetof(mibIfRow2{}.InOctets) - 1208
	_ = 1208 - unsafe.Offsetof(mibIfRow2{}.InOctets)

	_ = unsafe.Offsetof(mibIfRow2{}.OutOctets) - 1280
	_ = 1280 - unsafe.Offsetof(mibIfRow2{}.OutOctets)
)

// mibIfTable2 is MIB_IF_TABLE2: a count followed by that many rows.
type mibIfTable2 struct {
	NumEntries uint32
	_          uint32 // padding before the 8-byte-aligned rows
	Table      [1]mibIfRow2
}

// Interface is one network interface's identity and cumulative byte counters.
type Interface struct {
	Index       uint32
	Name        string // the connection name, e.g. "Ethernet" or "Wi-Fi"
	Description string // the adapter's own name
	Type        uint32
	Up          bool
	SpeedBits   uint64
	RxBytes     uint64
	TxBytes     uint64
	// Loopback and Virtual mark interfaces a person did not install: the
	// software loopback, and the tunnel/virtual adapters that VPNs and
	// hypervisors leave behind.
	Loopback bool
	Virtual  bool
}

// Interface types, from ifdef.h, for the few that need telling apart.
const (
	ifTypeOther            = 1
	ifTypeEthernet         = 6
	ifTypeSoftwareLoopback = 24
	ifTypeTunnel           = 131
	ifTypeIEEE80211        = 71
)

// ReadInterfaces returns every network interface with its byte counters.
func ReadInterfaces() ([]Interface, error) {
	// GetIfTable2 allocates the table itself and hands back a pointer to it,
	// which is why this takes the address of a pointer. It includes interfaces
	// that are not currently connected, which is what we want: an unplugged cable
	// should leave the row in place rather than make it disappear.
	var table *mibIfTable2
	r, _, err := procGetIfTable2.Call(uintptr(unsafe.Pointer(&table)))
	if r != 0 {
		return nil, fmt.Errorf("GetIfTable2: %w", err)
	}
	if table == nil {
		return nil, fmt.Errorf("GetIfTable2 returned no table")
	}
	defer procFreeMibTable.Call(uintptr(unsafe.Pointer(table)))

	n := int(table.NumEntries)
	if n == 0 {
		return nil, nil
	}
	rows := unsafe.Slice(&table.Table[0], n)

	out := make([]Interface, 0, n)
	for i := range rows {
		row := &rows[i]
		const ifOperStatusUp = 1
		it := Interface{
			Index:       row.InterfaceIndex,
			Name:        windows.UTF16ToString(row.Alias[:]),
			Description: windows.UTF16ToString(row.Description[:]),
			Type:        row.Type,
			Up:          row.OperStatus == ifOperStatusUp,
			SpeedBits:   row.ReceiveLinkSpeed,
			RxBytes:     row.InOctets,
			TxBytes:     row.OutOctets,
			Loopback:    row.Type == ifTypeSoftwareLoopback,
			Virtual:     row.Type == ifTypeTunnel,
		}
		out = append(out, it)
	}
	return out, nil
}

// IsWireless reports whether an interface type is Wi-Fi, so the UI can pick the
// right icon the way it does from /sys/class/net/*/wireless on Linux.
func (i Interface) IsWireless() bool { return i.Type == ifTypeIEEE80211 }
