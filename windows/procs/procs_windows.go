//go:build windows

// Package procs enumerates processes and installed applications and watches
// for process starts (Toolhelp snapshot polling; processes are keyed by
// (PID, CreateTime) to survive PID reuse).
package procs

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type Proc struct {
	PID     uint32
	PPID    uint32
	Name    string
	Path    string
	Created int64 // FILETIME as int64; 0 if unknown
	Session uint32
}

type Key struct {
	PID     uint32
	Created int64
}

func (p Proc) Key() Key { return Key{p.PID, p.Created} }

// ImagePath returns the Win32 path of a process image.
func ImagePath(pid uint32) (string, int64) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", 0
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	path := ""
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		path = windows.UTF16ToString(buf[:n])
	}
	var c, e, k, u windows.Filetime
	var created int64
	if windows.GetProcessTimes(h, &c, &e, &k, &u) == nil {
		created = int64(c.HighDateTime)<<32 | int64(c.LowDateTime)
	}
	return path, created
}

// Snapshot lists processes. withPaths resolves image paths (slower).
func Snapshot(withPaths bool) ([]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Proc
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		p := Proc{PID: e.ProcessID, PPID: e.ParentProcessID, Name: windows.UTF16ToString(e.ExeFile[:])}
		_ = windows.ProcessIdToSessionId(p.PID, &p.Session)
		if withPaths && p.PID > 4 {
			p.Path, p.Created = ImagePath(p.PID)
		}
		out = append(out, p)
	}
	return out, nil
}

// StartEvent is a newly observed process with its parent.
type StartEvent struct {
	Child  Proc
	Parent Proc
}

// Watch polls for new processes every interval and reports those whose
// parent is still alive (parent CreateTime <= child CreateTime).
func Watch(ctx context.Context, interval time.Duration, onStart func(StartEvent)) {
	known := map[uint32]Proc{}
	first := true
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		list, err := Snapshot(false)
		if err == nil {
			cur := make(map[uint32]Proc, len(list))
			var fresh []Proc
			for _, p := range list {
				if old, ok := known[p.PID]; ok && old.PPID == p.PPID && old.Name == p.Name {
					cur[p.PID] = old
					continue
				}
				if p.PID > 4 {
					p.Path, p.Created = ImagePath(p.PID)
				}
				cur[p.PID] = p
				fresh = append(fresh, p)
			}
			if !first {
				for _, p := range fresh {
					parent, ok := cur[p.PPID]
					if !ok {
						parent, ok = known[p.PPID] // parent already exited
					}
					if ok && p.Path != "" && parent.Path != "" &&
						(parent.Created == 0 || p.Created == 0 || parent.Created <= p.Created) {
						onStart(StartEvent{Child: p, Parent: parent})
					}
				}
			}
			known = cur
			first = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Installed lists Win32 applications from the Uninstall registry keys.
type Installed struct {
	Name, Path, Publisher string
}

func InstalledApps() []Installed {
	roots := []struct {
		k    registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	// per-user installs of every loaded profile (the service runs as SYSTEM)
	if users, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS); err == nil {
		names, _ := users.ReadSubKeyNames(-1)
		users.Close()
		for _, n := range names {
			if strings.HasPrefix(n, "S-1-5-21-") && !strings.HasSuffix(n, "_Classes") {
				roots = append(roots, struct {
					k    registry.Key
					path string
				}{registry.USERS, n + `\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`})
			}
		}
	}
	seen := map[string]bool{}
	var out []Installed
	for _, r := range roots {
		k, err := registry.OpenKey(r.k, r.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		subs, _ := k.ReadSubKeyNames(-1)
		for _, s := range subs {
			sk, err := registry.OpenKey(k, s, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, _ := sk.GetStringValue("DisplayName")
			icon, _, _ := sk.GetStringValue("DisplayIcon")
			loc, _, _ := sk.GetStringValue("InstallLocation")
			pub, _, _ := sk.GetStringValue("Publisher")
			sys, _, _ := sk.GetIntegerValue("SystemComponent")
			sk.Close()
			if name == "" || sys == 1 {
				continue
			}
			exe := exeFromIcon(icon)
			if exe == "" && loc != "" {
				exe = guessExe(loc, name)
			}
			if exe == "" || strings.Contains(strings.ToLower(filepath.Base(exe)), "unins") {
				continue
			}
			key := strings.ToLower(exe)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Installed{Name: name, Path: exe, Publisher: pub})
		}
		k.Close()
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func exeFromIcon(icon string) string {
	icon = strings.TrimSpace(icon)
	if i := strings.LastIndex(icon, ","); i > 0 {
		icon = icon[:i]
	}
	icon = strings.Trim(icon, `"`)
	if !strings.EqualFold(filepath.Ext(icon), ".exe") {
		return ""
	}
	if fileExists(icon) {
		return icon
	}
	return ""
}

func guessExe(dir, name string) string {
	entries, err := filepath.Glob(filepath.Join(dir, "*.exe"))
	if err != nil || len(entries) == 0 {
		return ""
	}
	lname := strings.ToLower(strings.ReplaceAll(name, " ", ""))
	for _, e := range entries {
		b := strings.ToLower(strings.TrimSuffix(filepath.Base(e), ".exe"))
		if strings.Contains(lname, b) && !strings.Contains(b, "unins") {
			return e
		}
	}
	if len(entries) == 1 {
		return entries[0]
	}
	return ""
}

func fileExists(p string) bool {
	a, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(p))
	return err == nil && a&windows.FILE_ATTRIBUTE_DIRECTORY == 0
}
