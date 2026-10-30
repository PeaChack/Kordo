//go:build windows

package procs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Process starts from ETW (Microsoft-Windows-Kernel-Process, ProcessStart).
// The kernel reports a start within milliseconds, while snapshot polling can
// miss a child that runs and connects between two snapshots.

var (
	advapi32             = windows.NewLazySystemDLL("advapi32.dll")
	procStartTraceW      = advapi32.NewProc("StartTraceW")
	procControlTraceW    = advapi32.NewProc("ControlTraceW")
	procEnableTraceEx2   = advapi32.NewProc("EnableTraceEx2")
	procOpenTraceW       = advapi32.NewProc("OpenTraceW")
	procProcessTrace     = advapi32.NewProc("ProcessTrace")
	procCloseTrace       = advapi32.NewProc("CloseTrace")
	kernelProcessGUID    = windows.GUID{Data1: 0x22FB2CD6, Data2: 0x0E7B, Data3: 0x422B, Data4: [8]byte{0xA0, 0xC7, 0x2F, 0xAD, 0x1F, 0xD0, 0xE7, 0x16}}
	etwSessionName       = "Kordo-ProcessStarts"
	errETWAlreadyRunning = errors.New("ETW process watcher already running")
)

const (
	wnodeFlagTracedGUID          = 0x00020000
	eventTraceRealTimeMode       = 0x00000100
	eventTraceControlStop        = 1
	eventControlCodeEnable       = 1
	traceLevelInformation        = 4
	keywordProcess               = 0x10 // WINEVENT_KEYWORD_PROCESS
	processTraceModeRealTime     = 0x00000100
	processTraceModeEventRecord  = 0x10000000
	invalidProcessTraceHandle    = ^uint64(0)
	eventIDProcessStart          = 1
	errorAlreadyExists           = 183
	etwPropsSize                 = 120 // sizeof(EVENT_TRACE_PROPERTIES) on 64-bit
	etwLoggerNameMax             = 256
	etwPropsBufferSize           = etwPropsSize + etwLoggerNameMax*2
	sizeofEventTraceLogfileAMD64 = 448
)

// EVENT_TRACE_PROPERTIES followed by room for the session name.
type etwProperties struct {
	// WNODE_HEADER
	BufferSize        uint32
	ProviderID        uint32
	HistoricalContext uint64
	TimeStamp         int64
	GUID              windows.GUID
	ClientContext     uint32
	Flags             uint32
	// EVENT_TRACE_PROPERTIES
	BufferSizeKB        uint32
	MinimumBuffers      uint32
	MaximumBuffers      uint32
	MaximumFileSize     uint32
	LogFileMode         uint32
	FlushTimer          uint32
	EnableFlags         uint32
	AgeLimit            int32
	NumberOfBuffers     uint32
	FreeBuffers         uint32
	EventsLost          uint32
	BuffersWritten      uint32
	LogBuffersLost      uint32
	RealTimeBuffersLost uint32
	LoggerThreadID      uintptr
	LogFileNameOffset   uint32
	LoggerNameOffset    uint32
	name                [etwLoggerNameMax]uint16
}

// EVENT_TRACE_LOGFILEW; the parts Kordo does not use are opaque.
type etwLogfile struct {
	LogFileName         *uint16
	LoggerName          *uint16
	CurrentTime         int64
	BuffersRead         uint32
	ProcessTraceMode    uint32
	_                   [88]byte  // EVENT_TRACE CurrentEvent
	_                   [280]byte // TRACE_LOGFILE_HEADER LogfileHeader
	BufferCallback      uintptr
	BufferSize          uint32
	Filled              uint32
	EventsLost          uint32
	EventRecordCallback uintptr
	IsKernelTrace       uint32
	Context             uintptr
}

// EVENT_RECORD
type etwEventRecord struct {
	Size           uint16
	HeaderType     uint16
	Flags          uint16
	EventProperty  uint16
	ThreadID       uint32
	ProcessID      uint32
	TimeStamp      int64
	ProviderID     windows.GUID
	ID             uint16
	Version        uint8
	Channel        uint8
	Level          uint8
	Opcode         uint8
	Task           uint16
	Keyword        uint64
	ProcessorTime  uint64
	ActivityID     windows.GUID
	BufferContext  uint32
	ExtendedCount  uint16
	UserDataLength uint16
	ExtendedData   uintptr
	UserData       unsafe.Pointer
	UserContext    uintptr
}

var etw struct {
	sync.Mutex
	running bool
	onStart func(StartEvent)
	paths   *ntPaths
	cache   map[uint32]Proc // recently started processes, for parents that already exited
}

// Counters for diagnostics (tests print them when no event arrives).
var etwStats struct {
	events, starts, noProps, noPath, noParent, emitted atomic.Uint64
	traceEnded                                         atomic.Bool
	traceResult                                        atomic.Uint64 // ProcessTrace return code once it ended
}

var etwCallback = syscall.NewCallback(func(rec *etwEventRecord) uintptr {
	etwStats.events.Add(1)
	if rec.ID != eventIDProcessStart || rec.ProviderID != kernelProcessGUID {
		return 0
	}
	etwStats.starts.Add(1)
	child, ok := readProcessStart(rec)
	if !ok {
		etwStats.noProps.Add(1)
		return 0
	}
	etw.Lock()
	cb := etw.onStart
	child.Path = etw.paths.toDOS(child.Path)
	if child.Path == "" {
		child.Path, _ = ImagePath(child.PID)
	}
	if len(etw.cache) > 4096 {
		etw.cache = map[uint32]Proc{}
	}
	parent, cached := etw.cache[child.PPID]
	etw.cache[child.PID] = child
	etw.Unlock()
	if cb == nil || child.Path == "" {
		etwStats.noPath.Add(1)
		return 0
	}
	// the parent is normally alive: ask it directly; fall back to what ETW saw
	if path, created := ImagePath(child.PPID); path != "" {
		parent = Proc{PID: child.PPID, Path: path, Created: created}
	} else if !cached {
		etwStats.noParent.Add(1)
		return 0
	}
	if parent.Created != 0 && child.Created != 0 && parent.Created > child.Created {
		etwStats.noParent.Add(1)
		return 0 // PID reused by a newer process: not the real parent
	}
	etwStats.emitted.Add(1)
	go cb(StartEvent{Child: child, Parent: parent})
	return 0
})

// ProcessStart fields are read by name through TDH: the payload layout
// differs between event versions (newer ones add sequence numbers and a
// SID before ImageName).
var (
	tdh                    = windows.NewLazySystemDLL("tdh.dll")
	procTdhGetPropertySize = tdh.NewProc("TdhGetPropertySize")
	procTdhGetProperty     = tdh.NewProc("TdhGetProperty")
	propProcessID          = utf16z("ProcessID")
	propParentProcessID    = utf16z("ParentProcessID")
	propCreateTime         = utf16z("CreateTime")
	propSessionID          = utf16z("SessionID")
	propImageName          = utf16z("ImageName")
)

func utf16z(s string) []uint16 {
	u, _ := windows.UTF16FromString(s)
	return u
}

// PROPERTY_DATA_DESCRIPTOR
type propertyDataDescriptor struct {
	PropertyName uint64
	ArrayIndex   uint32
	Reserved     uint32
}

func tdhProperty(rec *etwEventRecord, name []uint16) []byte {
	desc := propertyDataDescriptor{PropertyName: uint64(uintptr(unsafe.Pointer(&name[0]))), ArrayIndex: 0xFFFFFFFF}
	var size uint32
	r, _, _ := procTdhGetPropertySize.Call(uintptr(unsafe.Pointer(rec)), 0, 0, 1, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&size)))
	if r != 0 || size == 0 || size > 1<<16 {
		return nil
	}
	buf := make([]byte, size)
	r, _, _ = procTdhGetProperty.Call(uintptr(unsafe.Pointer(rec)), 0, 0, 1, uintptr(unsafe.Pointer(&desc)), uintptr(size), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return nil
	}
	return buf
}

func readProcessStart(rec *etwEventRecord) (Proc, bool) {
	le := binary.LittleEndian
	pid, ppid, ct := tdhProperty(rec, propProcessID), tdhProperty(rec, propParentProcessID), tdhProperty(rec, propCreateTime)
	if len(pid) < 4 || len(ppid) < 4 {
		return Proc{}, false
	}
	p := Proc{PID: le.Uint32(pid), PPID: le.Uint32(ppid)}
	if len(ct) >= 8 {
		p.Created = int64(le.Uint64(ct))
	}
	if sid := tdhProperty(rec, propSessionID); len(sid) >= 4 {
		p.Session = le.Uint32(sid)
	}
	p.Path = utf16Bytes(tdhProperty(rec, propImageName))
	p.Name = p.Path[strings.LastIndexAny(p.Path, `\/`)+1:]
	return p, true
}

func utf16Bytes(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return windows.UTF16ToString(u)
}

// WatchETW starts a real-time ETW session for process starts and calls
// onStart for each (on its own goroutine). It returns an error right away if
// the session cannot be started (then use Watch); otherwise it runs until ctx
// is done. Needs administrator or SYSTEM.
func WatchETW(ctx context.Context, onStart func(StartEvent)) error {
	etw.Lock()
	if etw.running {
		etw.Unlock()
		return errETWAlreadyRunning
	}
	etw.running = true
	etw.onStart = onStart
	etw.paths = newNTPaths()
	etw.cache = map[uint32]Proc{}
	etw.Unlock()
	fail := func(err error) error {
		etw.Lock()
		etw.running, etw.onStart = false, nil
		etw.Unlock()
		return err
	}
	if unsafe.Sizeof(etwLogfile{}) != sizeofEventTraceLogfileAMD64 || unsafe.Sizeof(etwEventRecord{}) != 112 {
		return fail(fmt.Errorf("ETW structures do not match this architecture (%s)", runtime.GOARCH))
	}

	name, _ := windows.UTF16FromString(etwSessionName)
	newProps := func() *etwProperties {
		p := &etwProperties{}
		p.BufferSize = etwPropsBufferSize
		p.Flags = wnodeFlagTracedGUID
		p.ClientContext = 1 // QPC timestamps
		p.LogFileMode = eventTraceRealTimeMode
		p.FlushTimer = 1 // seconds: deliver events promptly
		p.LoggerNameOffset = etwPropsSize
		return p
	}
	var session uint64
	start := func() uintptr {
		r, _, _ := procStartTraceW.Call(uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(newProps())))
		return r
	}
	r := start()
	if r == errorAlreadyExists { // left over from a crashed run
		stopETWSession(name)
		r = start()
	}
	if r != 0 {
		return fail(fmt.Errorf("StartTrace: %w", syscall.Errno(r)))
	}
	r, _, _ = procEnableTraceEx2.Call(uintptr(session), uintptr(unsafe.Pointer(&kernelProcessGUID)),
		eventControlCodeEnable, traceLevelInformation, keywordProcess, 0, 0, 0)
	if r != 0 {
		stopETWSession(name)
		return fail(fmt.Errorf("EnableTraceEx2: %w", syscall.Errno(r)))
	}
	logfile := &etwLogfile{
		LoggerName:          &name[0],
		ProcessTraceMode:    processTraceModeRealTime | processTraceModeEventRecord,
		EventRecordCallback: etwCallback,
	}
	h, _, err := procOpenTraceW.Call(uintptr(unsafe.Pointer(logfile)))
	if uint64(h) == invalidProcessTraceHandle {
		stopETWSession(name)
		return fail(fmt.Errorf("OpenTrace: %w", err))
	}

	done := make(chan struct{})
	go func() {
		// ProcessTrace blocks and calls etwCallback on this thread
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, _, _ := procProcessTrace.Call(uintptr(unsafe.Pointer(&h)), 1, 0, 0)
		etwStats.traceResult.Store(uint64(r))
		etwStats.traceEnded.Store(true)
		close(done)
	}()
	go func() {
		<-ctx.Done()
		procCloseTrace.Call(h)
		stopETWSession(name)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		etw.Lock()
		etw.running, etw.onStart = false, nil
		etw.Unlock()
	}()
	return nil
}

func stopETWSession(name []uint16) {
	p := &etwProperties{}
	p.BufferSize = etwPropsBufferSize
	p.LoggerNameOffset = etwPropsSize
	procControlTraceW.Call(0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(p)), eventTraceControlStop)
}

// ntPaths converts \Device\HarddiskVolumeN\... to C:\...
type ntPaths struct {
	devices map[string]string // `\Device\HarddiskVolume3` -> `C:`
	loaded  time.Time
}

func newNTPaths() *ntPaths {
	n := &ntPaths{}
	n.load()
	return n
}

func (n *ntPaths) load() {
	n.devices = map[string]string{}
	n.loaded = time.Now()
	buf := make([]uint16, 1024)
	for c := 'A'; c <= 'Z'; c++ {
		drive := string(c) + ":"
		d, _ := windows.UTF16PtrFromString(drive)
		if l, err := windows.QueryDosDevice(d, &buf[0], uint32(len(buf))); err == nil && l > 0 {
			n.devices[strings.ToLower(windows.UTF16ToString(buf[:l]))] = drive
		}
	}
}

func (n *ntPaths) toDOS(p string) string {
	if n == nil || !strings.HasPrefix(p, `\`) {
		return p
	}
	for try := 0; try < 2; try++ {
		lower := strings.ToLower(p)
		for dev, drive := range n.devices {
			if strings.HasPrefix(lower, dev+`\`) {
				return drive + p[len(dev):]
			}
		}
		if try == 0 && time.Since(n.loaded) > 10*time.Second {
			n.load() // a drive may have been added
			continue
		}
		break
	}
	return "" // unknown device: caller asks the process itself
}

// Dedup wraps onStart so that a process reported by both ETW and polling is
// handled once.
func Dedup(onStart func(StartEvent)) func(StartEvent) {
	var mu sync.Mutex
	seen := map[Key]time.Time{}
	return func(ev StartEvent) {
		k := ev.Child.Key()
		mu.Lock()
		if _, ok := seen[k]; ok {
			mu.Unlock()
			return
		}
		now := time.Now()
		seen[k] = now
		if len(seen) > 4096 {
			for key, t := range seen {
				if now.Sub(t) > 10*time.Minute {
					delete(seen, key)
				}
			}
		}
		mu.Unlock()
		onStart(ev)
	}
}
