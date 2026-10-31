//go:build windows

package platform

// Windows event recorder: a kernel ETW session (system logger mode, Windows 8 and later, needs
// admin) for the stalls nothing inside the game explains. It records which drivers ran DPCs and
// interrupts (and for how long), hard page faults per process (memory read back from disk) and
// disk I/O per process. Events are summed into 10 ms buckets on Frame Lab's QPC clock and written
// to windows.csv; a summary goes to windows_stats.json when the session stops.
//
// Everything here only reads: the session is started for the recording and stopped after it.
// A session left over by a crash (same name) is stopped before a new one starts.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32         = windows.NewLazySystemDLL("advapi32.dll")
	procStartTrace   = advapi32.NewProc("StartTraceW")
	procControl      = advapi32.NewProc("ControlTraceW")
	procOpenTrace    = advapi32.NewProc("OpenTraceW")
	procProcess      = advapi32.NewProc("ProcessTrace")
	procCloseTrace   = advapi32.NewProc("CloseTrace")
	procTraceSetInfo = advapi32.NewProc("TraceSetInformation")
)

const winEvSession = "NightCityFrameLab Windows events"

const (
	wnodeTracedGuid    = 0x00020000
	modeRealTime       = 0x00000100
	modeSystemLogger   = 0x02000000
	ctlQuery           = 0
	ctlStop            = 1
	kFlagProcess       = 0x00000001
	kFlagThread        = 0x00000002
	kFlagImage         = 0x00000004
	kFlagDiskIO        = 0x00000100
	kFlagHardFaults    = 0x00002000
	kFlagDPC           = 0x00020000
	kFlagInterrupt     = 0x00040000
	perfMask1DPC       = 0x00000080 // group mask word 1
	perfMask1Interrupt = 0x00004000
	ptmRealTime        = 0x00000100
	ptmRawTimestamp    = 0x00001000
	ptmEventRecord     = 0x10000000
	errAlreadyExists   = 183
	propsBase          = 120 // sizeof(EVENT_TRACE_PROPERTIES) on x64
	logfileSize        = 448 // sizeof(EVENT_TRACE_LOGFILEW) on x64
	invalidTraceHandle = ^uint64(0)
)

// classic kernel event classes
var (
	guidPerfInfo  = windows.GUID{Data1: 0xce1dbfb4, Data2: 0x137e, Data3: 0x4da6, Data4: [8]byte{0x87, 0xb0, 0x3f, 0x59, 0xaa, 0x10, 0x2c, 0xbc}}
	guidImage     = windows.GUID{Data1: 0x2cb15d1d, Data2: 0x5fc1, Data3: 0x11d2, Data4: [8]byte{0xab, 0xe1, 0x00, 0xa0, 0xc9, 0x11, 0xf5, 0x18}}
	guidThread    = windows.GUID{Data1: 0x3d6fa8d1, Data2: 0xfe05, Data3: 0x11d0, Data4: [8]byte{0x9d, 0xda, 0x00, 0xc0, 0x4f, 0xd7, 0xba, 0x7c}}
	guidPageFault = windows.GUID{Data1: 0x3d6fa8d3, Data2: 0xfe05, Data3: 0x11d0, Data4: [8]byte{0x9d, 0xda, 0x00, 0xc0, 0x4f, 0xd7, 0xba, 0x7c}}
	guidDiskIo    = windows.GUID{Data1: 0x3d6fa8d4, Data2: 0xfe05, Data3: 0x11d0, Data4: [8]byte{0x9d, 0xda, 0x00, 0xc0, 0x4f, 0xd7, 0xba, 0x7c}}
	// this session's own id
	guidSession = windows.GUID{Data1: 0x6b2f7e31, Data2: 0x9c4d, Data3: 0x4a8e, Data4: [8]byte{0x8f, 0x21, 0x5e, 0x3a, 0x77, 0x0c, 0x41, 0xd2}}
)

// event kinds in windows.csv
const (
	wkDPC uint8 = iota + 1
	wkISR
	wkHardFault
	wkDiskRead
	wkDiskWrite
)

var wkName = map[uint8]string{wkDPC: "dpc", wkISR: "isr", wkHardFault: "hardfault", wkDiskRead: "diskread", wkDiskWrite: "diskwrite"}

type wkKey struct {
	b    int64 // bucket index (10 ms)
	kind uint8
	id   uint64 // driver index (dpc/isr) or pid
}

type wkAcc struct {
	n     uint32
	sum   int64 // QPC ticks (dpc/isr/hardfault time, disk response time)
	max   int64
	bytes uint64
}

type kImage struct {
	base, end uint64
	name      string
}

type winEvRec struct {
	path     string
	gamePid  uint32
	freq     int64
	bucket   int64 // ticks per bucket
	session  uint64
	trace    uint64
	props    []byte
	namePtr  *uint16
	logfile  []byte
	done     chan struct{}
	stopOnce sync.Once

	mu       sync.Mutex
	acc      map[wkKey]*wkAcc
	lastB    int64
	images   []kImage // kernel modules, sorted by base
	imgDirty bool
	drivers  []string // driver index -> name
	drvIdx   map[string]int
	tid2pid  map[uint32]uint32
	names    map[uint32]string
	counts   map[string]uint64
	drvTot   []DriverTotal // by driver index, in QPC ticks until Stop
	rows     uint64
	firstTs  int64
	lastTs   int64
	f        *os.File
	w        *bufio.Writer
	errText  string
}

var (
	winEvCurrent atomic.Pointer[winEvRec]
	winEvCB      uintptr
	winEvCBOnce  sync.Once
)

// WinEvents is a running Windows event recording.
type WinEvents interface {
	Stop() WinEventStats
}

type DriverTotal struct {
	Name    string `json:"name"`
	TotalUs int64  `json:"total_us"`
	MaxUs   int64  `json:"max_us"`
	Count   uint64 `json:"count"`
}

type WinEventStats struct {
	Events       map[string]uint64 `json:"events"`
	Rows         uint64            `json:"rows"`
	EventsLost   uint32            `json:"events_lost"`
	BuffersLost  uint32            `json:"buffers_lost"`
	Drivers      int               `json:"drivers_known"`
	DriverTotals []DriverTotal     `json:"driver_totals,omitempty"` // every DPC / interrupt of the recording
	FirstQpc     int64             `json:"first_qpc"`
	LastQpc      int64             `json:"last_qpc"`
	Error        string            `json:"error,omitempty"`
}

func utf16At(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return windows.UTF16ToString(u)
}

func newWinEvProps(flags uint32) []byte {
	b := make([]byte, propsBase+1024+1024)
	le := binary.LittleEndian
	le.PutUint32(b[0:], uint32(len(b)))
	le.PutUint32(b[24:], guidSession.Data1)
	le.PutUint16(b[28:], guidSession.Data2)
	le.PutUint16(b[30:], guidSession.Data3)
	copy(b[32:40], guidSession.Data4[:])
	le.PutUint32(b[40:], 1) // ClientContext 1: QPC timestamps (same clock as the rest of Frame Lab)
	le.PutUint32(b[44:], wnodeTracedGuid)
	le.PutUint32(b[48:], 256) // buffer size, KB
	le.PutUint32(b[52:], 64)  // minimum buffers
	le.PutUint32(b[56:], 512) // maximum buffers
	le.PutUint32(b[64:], modeRealTime|modeSystemLogger)
	le.PutUint32(b[68:], 1) // flush every second
	le.PutUint32(b[72:], flags)
	le.PutUint32(b[116:], propsBase) // LoggerNameOffset
	return b
}

// StopWinEvents stops a Windows event session of ours left running (e.g. after a crash).
func StopWinEvents() {
	name, _ := windows.UTF16PtrFromString(winEvSession)
	p := newWinEvProps(0)
	procControl.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&p[0])), ctlStop)
	runtime.KeepAlive(p)
}

// StartWinEvents starts the kernel session and a consumer that writes windows.csv to out.
func StartWinEvents(gamePid uint32, out string) (WinEvents, error) {
	if !IsAdmin() {
		return nil, fmt.Errorf("needs Frame Lab to run as administrator")
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	r := &winEvRec{path: out, gamePid: gamePid, freq: QPCFreq(), done: make(chan struct{}),
		acc: map[wkKey]*wkAcc{}, drvIdx: map[string]int{}, tid2pid: map[uint32]uint32{}, names: processes(),
		counts: map[string]uint64{}, f: f, w: bufio.NewWriterSize(f, 1<<16)}
	r.bucket = r.freq / 100
	if r.bucket <= 0 {
		r.bucket = 100000
	}
	fmt.Fprintln(r.w, "qpc,kind,name,pid,count,sum_us,max_us,bytes")

	name, _ := windows.UTF16PtrFromString(winEvSession)
	r.namePtr = name
	flags := uint32(kFlagProcess | kFlagThread | kFlagImage | kFlagDiskIO | kFlagHardFaults | kFlagDPC | kFlagInterrupt)
	r.props = newWinEvProps(flags)
	ret, _, _ := procStartTrace.Call(uintptr(unsafe.Pointer(&r.session)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&r.props[0])))
	if ret == errAlreadyExists {
		StopWinEvents()
		r.props = newWinEvProps(flags)
		ret, _, _ = procStartTrace.Call(uintptr(unsafe.Pointer(&r.session)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&r.props[0])))
	}
	if ret != 0 {
		f.Close()
		os.Remove(out)
		return nil, fmt.Errorf("StartTrace: %v", syscall.Errno(ret))
	}
	// the kernel groups for this system logger session, as a PERFINFO_GROUPMASK: the first word takes
	// the classic EnableFlags bits (process, thread, image, disk, hard faults), but DPCs and interrupts
	// live in the second word (PERF_DPC 0x20000080, PERF_INTERRUPT 0x20004000). 1.4.0 put the classic
	// DPC/interrupt bits in the first word, which records nothing.
	mask := [8]uint32{flags &^ (kFlagDPC | kFlagInterrupt), perfMask1DPC | perfMask1Interrupt}
	if ret, _, _ := procTraceSetInfo.Call(uintptr(r.session), 4 /* TraceSystemTraceEnableFlagsInfo */, uintptr(unsafe.Pointer(&mask[0])), 32); ret != 0 {
		r.errText = fmt.Sprintf("TraceSetInformation: %v", syscall.Errno(ret))
	}

	winEvCBOnce.Do(func() { winEvCB = syscall.NewCallback(winEvCallback) })
	r.logfile = make([]byte, logfileSize)
	le := binary.LittleEndian
	le.PutUint64(r.logfile[8:], uint64(uintptr(unsafe.Pointer(name)))) // LoggerName
	le.PutUint32(r.logfile[28:], ptmRealTime|ptmEventRecord|ptmRawTimestamp)
	le.PutUint64(r.logfile[424:], uint64(winEvCB)) // EventRecordCallback
	h, _, _ := procOpenTrace.Call(uintptr(unsafe.Pointer(&r.logfile[0])))
	r.trace = uint64(h)
	if r.trace == invalidTraceHandle || r.trace == 0 {
		r.stopSession()
		f.Close()
		os.Remove(out)
		return nil, fmt.Errorf("OpenTrace failed")
	}
	winEvCurrent.Store(r)
	go func() {
		runtime.LockOSThread()
		defer close(r.done)
		procProcess.Call(uintptr(unsafe.Pointer(&r.trace)), 1, 0, 0) // returns when the session stops
	}()
	go r.flushLoop()
	return r, nil
}

func (r *winEvRec) stopSession() (lost, bufLost uint32) {
	p := newWinEvProps(0)
	procControl.Call(uintptr(r.session), 0, uintptr(unsafe.Pointer(&p[0])), ctlStop)
	lost = binary.LittleEndian.Uint32(p[88:])
	bufLost = binary.LittleEndian.Uint32(p[100:])
	runtime.KeepAlive(p)
	return
}

func (r *winEvRec) Stop() WinEventStats {
	var st WinEventStats
	r.stopOnce.Do(func() {
		st.EventsLost, st.BuffersLost = r.stopSession()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			procCloseTrace.Call(uintptr(r.trace))
			select {
			case <-r.done:
			case <-time.After(3 * time.Second):
			}
		}
		procCloseTrace.Call(uintptr(r.trace))
		winEvCurrent.CompareAndSwap(r, nil)
		r.mu.Lock()
		r.flush(1 << 62)
		st.Events = r.counts
		st.Rows = r.rows
		st.Drivers = len(r.images)
		for i, d := range r.drvTot {
			if i < len(r.drivers) && d.Count > 0 {
				d.Name = r.drivers[i]
				d.TotalUs, d.MaxUs = d.TotalUs*1_000_000/r.freq, d.MaxUs*1_000_000/r.freq
				st.DriverTotals = append(st.DriverTotals, d)
			}
		}
		sort.Slice(st.DriverTotals, func(i, j int) bool { return st.DriverTotals[i].TotalUs > st.DriverTotals[j].TotalUs })
		st.FirstQpc, st.LastQpc = r.firstTs, r.lastTs
		st.Error = r.errText
		r.w.Flush()
		r.f.Close()
		r.mu.Unlock()
		b, _ := json.MarshalIndent(st, "", " ")
		os.WriteFile(filepath.Join(filepath.Dir(r.path), "windows_stats.json"), b, 0o644)
		runtime.KeepAlive(r.namePtr)
		runtime.KeepAlive(r.logfile)
	})
	return st
}

// flushLoop writes buckets older than 3 seconds (events from different CPUs arrive a little out of
// order) and refreshes the process names now and then.
func (r *winEvRec) flushLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-r.done:
			return
		case <-t.C:
		}
		n++
		var names map[uint32]string
		if n%5 == 0 {
			names = processes()
		}
		r.mu.Lock()
		if names != nil {
			for k, v := range names {
				r.names[k] = v
			}
		}
		r.flush(r.lastB - 300)
		r.mu.Unlock()
	}
}

// flush writes (and forgets) every bucket before b; call with mu held.
func (r *winEvRec) flush(before int64) {
	var keys []wkKey
	for k := range r.acc {
		if k.b < before {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].b != keys[j].b {
			return keys[i].b < keys[j].b
		}
		return keys[i].kind < keys[j].kind
	})
	us := func(t int64) int64 { return t * 1_000_000 / r.freq }
	for _, k := range keys {
		a := r.acc[k]
		delete(r.acc, k)
		sumUs, maxUs := us(a.sum), us(a.max)
		name, pid := "", uint32(0)
		switch k.kind {
		case wkDPC, wkISR:
			if sumUs < 100 && maxUs < 100 {
				continue
			}
			if int(k.id) < len(r.drivers) {
				name = r.drivers[k.id]
			}
		case wkDiskRead, wkDiskWrite:
			pid = uint32(k.id)
			if a.bytes < 1<<20 && maxUs < 10_000 && pid != r.gamePid {
				continue
			}
		default:
			pid = uint32(k.id)
		}
		if k.kind != wkDPC && k.kind != wkISR {
			name = r.names[pid]
			if pid == 0 {
				name = "System"
			} else if name == "" {
				name = "pid " + strconv.Itoa(int(pid))
			}
		}
		fmt.Fprintf(r.w, "%d,%s,%s,%d,%d,%d,%d,%d\n", k.b*r.bucket, wkName[k.kind], strings.ReplaceAll(name, ",", " "), pid, a.n, sumUs, maxUs, a.bytes)
		r.rows++
	}
}

func winEvCallback(rec uintptr) uintptr {
	if r := winEvCurrent.Load(); r != nil {
		r.onEvent(unsafe.Pointer(rec))
	}
	return 0
}

func (r *winEvRec) driverOf(addr uint64) int {
	if r.imgDirty {
		sort.Slice(r.images, func(i, j int) bool { return r.images[i].base < r.images[j].base })
		r.imgDirty = false
	}
	i := sort.Search(len(r.images), func(i int) bool { return r.images[i].base > addr }) - 1
	name := "unknown driver"
	if i >= 0 && addr < r.images[i].end {
		name = r.images[i].name
	}
	idx, ok := r.drvIdx[name]
	if !ok {
		idx = len(r.drivers)
		r.drivers = append(r.drivers, name)
		r.drvIdx[name] = idx
	}
	return idx
}

func (r *winEvRec) add(ts int64, kind uint8, id uint64, dur int64, bytes uint64) {
	k := wkKey{ts / r.bucket, kind, id}
	a := r.acc[k]
	if a == nil {
		a = &wkAcc{}
		r.acc[k] = a
	}
	a.n++
	if dur > 0 {
		a.sum += dur
		if dur > a.max {
			a.max = dur
		}
	}
	a.bytes += bytes
	if k.b > r.lastB {
		r.lastB = k.b
	}
}

func (r *winEvRec) onEvent(p unsafe.Pointer) {
	defer func() {
		if e := recover(); e != nil {
			r.errText = fmt.Sprint(e)
		}
	}()
	hdrFlags := *(*uint16)(unsafe.Add(p, 4))
	if hdrFlags&0x0040 == 0 { // only 64-bit event payloads
		return
	}
	ts := *(*int64)(unsafe.Add(p, 16))
	prov := *(*windows.GUID)(unsafe.Add(p, 24))
	opcode := *(*uint8)(unsafe.Add(p, 45))
	n := int(*(*uint16)(unsafe.Add(p, 86)))
	ud := *(*unsafe.Pointer)(unsafe.Add(p, 96))
	if ud == nil || n == 0 {
		return
	}
	d := unsafe.Slice((*byte)(ud), n)
	le := binary.LittleEndian

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.firstTs == 0 {
		r.firstTs = ts
	}
	if ts > r.lastTs {
		r.lastTs = ts
	}
	switch prov {
	case guidPerfInfo:
		if n < 16 {
			return
		}
		var kind uint8
		switch opcode {
		case 66, 68, 69: // threaded DPC, DPC, timer DPC
			kind = wkDPC
		case 67, 50: // ISR, MSI ISR
			kind = wkISR
		default:
			return
		}
		start := int64(le.Uint64(d[0:]))
		dur := ts - start
		if dur < 0 || dur > r.freq {
			return
		}
		r.counts[wkName[kind]]++
		di := r.driverOf(le.Uint64(d[8:]))
		for len(r.drvTot) <= di {
			r.drvTot = append(r.drvTot, DriverTotal{})
		}
		t := &r.drvTot[di]
		t.TotalUs += dur // ticks for now
		if dur > t.MaxUs {
			t.MaxUs = dur
		}
		t.Count++
		r.add(start, kind, uint64(di), dur, 0)
	case guidImage:
		if (opcode != 10 && opcode != 3) || n < 58 {
			return
		}
		base, size := le.Uint64(d[0:]), le.Uint64(d[8:])
		if base < 0xFFFF800000000000 { // kernel modules (drivers) only
			return
		}
		name := utf16At(d[56:])
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		r.images = append(r.images, kImage{base, base + size, name})
		r.imgDirty = true
		r.counts["image"]++
	case guidThread:
		if (opcode != 1 && opcode != 3) || n < 8 {
			return
		}
		r.tid2pid[le.Uint32(d[4:])] = le.Uint32(d[0:])
	case guidPageFault:
		if opcode != 32 || n < 40 {
			return
		}
		start := int64(le.Uint64(d[0:]))
		dur := ts - start
		if dur < 0 || dur > 10*r.freq {
			dur = 0
		}
		pid := r.tid2pid[le.Uint32(d[32:])]
		r.counts["hardfault"]++
		r.add(ts-dur, wkHardFault, uint64(pid), dur, uint64(le.Uint32(d[36:])))
	case guidDiskIo:
		if (opcode != 10 && opcode != 11) || n < 52 {
			return
		}
		kind := wkDiskRead
		if opcode == 11 {
			kind = wkDiskWrite
		}
		pid := r.tid2pid[le.Uint32(d[48:])]
		resp := int64(le.Uint64(d[40:]))
		if resp < 0 || resp > 60*r.freq {
			resp = 0
		}
		r.counts[wkName[kind]]++
		r.add(ts-resp, kind, uint64(pid), resp, uint64(le.Uint32(d[8:])))
	}
}
