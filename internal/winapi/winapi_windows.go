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
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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

// CPUTimes is one logical processor's cumulative time, in the 100-nanosecond
// units Windows counts in.
//
// The raw counts are kept rather than converted to seconds: the only thing done
// with them is a ratio of differences between two samples, and integers that only
// ever increase are the right shape for that.
//
// Busy excludes idle. Windows reports KernelTime with idle *included*, which is
// the single easiest thing to get wrong here — left uncorrected, every core reads
// as permanently busy — so the subtraction happens once, on the way out.
type CPUTimes struct {
	Idle  uint64
	Busy  uint64
	Total uint64
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
			idle := uint64(p.IdleTime)
			kernel := uint64(p.KernelTime)
			user := uint64(p.UserTime)
			// KernelTime includes IdleTime, so the busy part of it is the
			// difference. Guarded because these are read without a lock and a
			// sample torn across the two fields would otherwise underflow into an
			// enormous number.
			busy := user
			if kernel > idle {
				busy += kernel - idle
			}
			out[i] = CPUTimes{Idle: idle, Busy: busy, Total: idle + busy}
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
	MAC         string
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
			MAC:         macString(row.PhysicalAddress[:], row.PhysicalAddressLength),
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

// ---------------------------------------------------------------- disks

// diskPerformance is DISK_PERFORMANCE, the counters the storage stack keeps per
// volume. Only the byte totals and the device name are read.
type diskPerformance struct {
	BytesRead           int64
	BytesWritten        int64
	ReadTime            int64
	WriteTime           int64
	IdleTime            int64
	ReadCount           uint32
	WriteCount          uint32
	QueueDepth          uint32
	SplitCount          uint32
	QueryTime           int64
	StorageDeviceNumber uint32
	StorageManagerName  [8]uint16
}

// IOCTL_DISK_PERFORMANCE, assembled the way CTL_CODE does:
// (IOCTL_DISK_BASE << 16) | (FILE_READ_ACCESS << 14) | (0x0008 << 2) | METHOD_BUFFERED.
const ioctlDiskPerformance = (0x00000007 << 16) | (0x0001 << 14) | (0x0008 << 2) | 0

// DiskIO is a volume's cumulative bytes read and written.
type DiskIO struct {
	ReadBytes  uint64
	WriteBytes uint64
}

// ReadDiskIO asks a volume for its own byte counters.
//
// volume is a drive letter with no trailing separator, as in `C:`. The handle is
// opened asking for no access at all, which is what lets this work without
// administrator rights: querying performance counters needs the handle to exist,
// not permission to read the data on it.
//
// The counters come from the partition manager and are only kept while disk
// performance counters are enabled. They are on by default on current Windows,
// but a machine where someone has turned them off reports nothing rather than
// zero — hence the boolean.
func ReadDiskIO(volume string) (DiskIO, bool) {
	path, err := windows.UTF16PtrFromString(`\\.\` + volume)
	if err != nil {
		return DiskIO{}, false
	}
	h, err := windows.CreateFile(path, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return DiskIO{}, false
	}
	defer windows.CloseHandle(h)

	var perf diskPerformance
	var got uint32
	err = windows.DeviceIoControl(h, ioctlDiskPerformance,
		nil, 0,
		(*byte)(unsafe.Pointer(&perf)), uint32(unsafe.Sizeof(perf)),
		&got, nil)
	if err != nil {
		return DiskIO{}, false
	}
	return DiskIO{ReadBytes: uint64(perf.BytesRead), WriteBytes: uint64(perf.BytesWritten)}, true
}

// Volume is one mounted volume: a drive letter and what is on it.
type Volume struct {
	// Root is the path with its separator, as in `C:\`; Letter is `C:`.
	Root   string
	Letter string
	// Label is the volume's name, and FileSystem is NTFS, FAT32 and so on.
	Label      string
	FileSystem string
	// Fixed is false for removable media, network shares and optical drives.
	Fixed bool
	// Remote marks a network share, which is worth telling apart: measuring one
	// can block for as long as the server takes to answer.
	Remote bool

	TotalBytes uint64
	FreeBytes  uint64
}

// Drive types from GetDriveType.
const (
	driveRemovable = 2
	driveFixed     = 3
	driveRemote    = 4
	driveCDROM     = 5
)

// ReadVolumes enumerates the mounted volumes with their size and free space.
//
// A drive that is present but has no medium in it — an empty card reader, an
// optical drive with the tray open — answers the size query with an error, and is
// left out rather than listed as a disk of zero bytes.
func ReadVolumes() ([]Volume, error) {
	buf := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return nil, fmt.Errorf("GetLogicalDriveStrings: %w", err)
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n)
		if _, err = windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0]); err != nil {
			return nil, fmt.Errorf("GetLogicalDriveStrings: %w", err)
		}
	}

	var out []Volume
	for _, root := range splitNulUTF16(buf) {
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		kind := windows.GetDriveType(rootPtr)
		if kind == driveCDROM {
			continue // an optical drive is not something to chart
		}

		v := Volume{
			Root:   root,
			Letter: strings.TrimSuffix(root, `\`),
			Fixed:  kind == driveFixed,
			Remote: kind == driveRemote,
		}
		if kind == driveRemovable && !hasMedium(rootPtr) {
			continue
		}

		var free, total uint64
		if err := windows.GetDiskFreeSpaceEx(rootPtr, nil, &total, &free); err != nil {
			continue // no medium, or not ready
		}
		v.TotalBytes, v.FreeBytes = total, free

		label := make([]uint16, 261)
		fsName := make([]uint16, 261)
		if err := windows.GetVolumeInformation(rootPtr,
			&label[0], uint32(len(label)), nil, nil, nil,
			&fsName[0], uint32(len(fsName))); err == nil {
			v.Label = windows.UTF16ToString(label)
			v.FileSystem = windows.UTF16ToString(fsName)
		}
		out = append(out, v)
	}
	return out, nil
}

// hasMedium reports whether a removable drive has anything in it, without
// provoking the "please insert a disk" dialog that a bare query would.
func hasMedium(root *uint16) bool {
	old := windows.SetErrorMode(windows.SEM_FAILCRITICALERRORS)
	defer windows.SetErrorMode(old)
	var free, total uint64
	return windows.GetDiskFreeSpaceEx(root, nil, &total, &free) == nil
}

// splitNulUTF16 splits the NUL-separated, double-NUL-terminated list that several
// Windows calls return into strings.
func splitNulUTF16(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
			break // the second NUL: end of the list
		}
		out = append(out, windows.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	return out
}

// SystemDrive is the drive Windows is installed on, as `C:` with no separator.
// It comes from the environment because that is where Windows itself publishes
// it, and it is not always C.
func SystemDrive() string {
	return strings.TrimSuffix(os.Getenv("SystemDrive"), `\`)
}

// VolumeSpace returns a volume's total and available bytes. root is a path with
// its separator, as in `C:\`.
//
// Available is what this user may actually use, which on a volume with quotas is
// less than what is unallocated. That matches statfs's Bavail on Linux, which is
// also the caller-visible figure rather than the raw one.
func VolumeSpace(root string) (total, available uint64, ok bool) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0, false
	}
	old := windows.SetErrorMode(windows.SEM_FAILCRITICALERRORS)
	defer windows.SetErrorMode(old)

	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &available, &total, &free); err != nil {
		return 0, 0, false
	}
	return total, available, true
}

// ---------------------------------------------------------------- processor detail

var (
	powrprof                    = windows.NewLazySystemDLL("powrprof.dll")
	procCallNtPowerInformation  = powrprof.NewProc("CallNtPowerInformation")
	procGetLogicalProcessorInfo = kernel32.NewProc("GetLogicalProcessorInformationEx")
)

// processorPowerInformation is PROCESSOR_POWER_INFORMATION, one per logical
// processor.
type processorPowerInformation struct {
	Number           uint32
	MaxMhz           uint32
	CurrentMhz       uint32
	MhzLimit         uint32
	MaxIdleState     uint32
	CurrentIdleState uint32
}

const (
	_ = unsafe.Sizeof(processorPowerInformation{}) - 24
	_ = 24 - unsafe.Sizeof(processorPowerInformation{})
)

// processorInformationLevel is the ProcessorInformation information level for
// CallNtPowerInformation.
const processorInformationLevel = 11

// ReadFrequencies returns each logical processor's current and maximum clock in
// MHz.
//
// This is the honest source for a live frequency on Windows. The registry's ~MHz
// is the clock the machine booted at and never changes, so a processor sitting at
// 800 MHz or boosting to 4.8 GHz would read identically — which is the whole
// thing the reading is for.
func ReadFrequencies() (current, max []uint32, err error) {
	n := runtime.NumCPU()
	buf := make([]processorPowerInformation, n)
	size := uint32(n) * uint32(unsafe.Sizeof(processorPowerInformation{}))

	// The first two arguments are for setting information, which is not what this
	// is doing, so they are nil.
	r, _, callErr := procCallNtPowerInformation.Call(
		uintptr(processorInformationLevel),
		0, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(size))
	// CallNtPowerInformation returns an NTSTATUS: zero is success.
	if r != 0 {
		return nil, nil, fmt.Errorf("CallNtPowerInformation: status %#x (%v)", r, callErr)
	}

	current = make([]uint32, n)
	max = make([]uint32, n)
	for i := range buf {
		current[i] = buf[i].CurrentMhz
		max[i] = buf[i].MaxMhz
	}
	return current, max, nil
}

// Relationship values for GetLogicalProcessorInformationEx.
const (
	relationProcessorCore    = 0
	relationCache            = 2
	relationProcessorPackage = 3
	relationAll              = 0xffff
)

// Cache types, from LOGICAL_PROCESSOR_RELATIONSHIP's CACHE_RELATIONSHIP.
const (
	cacheUnified     = 0
	cacheInstruction = 1
	cacheData        = 2
)

// Topology is how many of each thing the machine has, and the size of one core's
// caches in bytes.
type Topology struct {
	Logical   int
	PhysCores int
	Sockets   int

	L1D, L1I, L2, L3 uint64
}

// ReadTopology counts cores, sockets and caches.
//
// The records are variable-length and self-describing: each carries its own size,
// so the walk steps by that rather than by any structure's size. Only the few
// fields that are read are decoded, at fixed offsets from the start of a record,
// which avoids transcribing three union members whose layouts would then all have
// to be kept right.
func ReadTopology() (Topology, error) {
	t := Topology{Logical: runtime.NumCPU()}

	var size uint32
	r, _, _ := procGetLogicalProcessorInfo.Call(uintptr(relationAll), 0, uintptr(unsafe.Pointer(&size)))
	if r != 0 && size == 0 {
		return t, fmt.Errorf("GetLogicalProcessorInformationEx: no size")
	}
	buf := make([]byte, size)
	r, _, err := procGetLogicalProcessorInfo.Call(
		uintptr(relationAll), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return t, fmt.Errorf("GetLogicalProcessorInformationEx: %w", err)
	}

	// Caches are reported per core, so the same L1 appears once for every core on
	// the machine. Only the first of each level is kept: the figure Atlas shows is
	// "how big is a core's L1", not the sum across the package.
	for off := 0; off+8 <= int(size); {
		rel := *(*uint32)(unsafe.Pointer(&buf[off]))
		recSize := int(*(*uint32)(unsafe.Pointer(&buf[off+4])))
		if recSize < 8 || off+recSize > int(size) {
			break // a malformed record must not spin or read past the buffer
		}
		body := off + 8

		switch rel {
		case relationProcessorCore:
			t.PhysCores++
		case relationProcessorPackage:
			t.Sockets++
		case relationCache:
			// CACHE_RELATIONSHIP: Level, Associativity, LineSize, CacheSize, Type.
			if body+16 <= off+recSize {
				level := buf[body]
				cacheSize := uint64(*(*uint32)(unsafe.Pointer(&buf[body+4])))
				cacheType := *(*uint32)(unsafe.Pointer(&buf[body+8]))
				switch {
				case level == 1 && cacheType == cacheData && t.L1D == 0:
					t.L1D = cacheSize
				case level == 1 && cacheType == cacheInstruction && t.L1I == 0:
					t.L1I = cacheSize
				case level == 1 && cacheType == cacheUnified && t.L1D == 0:
					// A unified L1 counts as both rather than neither.
					t.L1D, t.L1I = cacheSize, cacheSize
				case level == 2 && t.L2 == 0:
					t.L2 = cacheSize
				case level == 3 && t.L3 == 0:
					t.L3 = cacheSize
				}
			}
		}
		off += recSize
	}
	return t, nil
}

// ProcessorName is the model string, as the firmware reported it.
func ProcessorName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	name, _, err := k.GetStringValue("ProcessorNameString")
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(name), " ") // collapse the padding it comes with
}

// macString formats a hardware address the way Linux writes it in sysfs, so both
// platforms hand the UI the same shape of string. An interface with no hardware
// address — a tunnel, the loopback — gets "".
func macString(addr []byte, n uint32) string {
	if n == 0 || int(n) > len(addr) {
		return ""
	}
	var b strings.Builder
	for i := 0; i < int(n); i++ {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02x", addr[i])
	}
	return b.String()
}
