//go:build windows

package procs

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNTPaths(t *testing.T) {
	n := newNTPaths()
	if len(n.devices) == 0 {
		t.Skip("no drives reported")
	}
	for dev, drive := range n.devices {
		got := n.toDOS(dev + `\x\y.exe`)
		if !strings.EqualFold(got, drive+`\x\y.exe`) {
			t.Fatalf("toDOS(%s) = %q, want %s", dev, got, drive+`\x\y.exe`)
		}
	}
	if got := n.toDOS(`\Device\NoSuchDevice\a.exe`); got != "" {
		t.Fatalf("unknown device: %q", got)
	}
}

func TestDedup(t *testing.T) {
	n := 0
	f := Dedup(func(StartEvent) { n++ })
	ev := StartEvent{Child: Proc{PID: 7, Created: 9}}
	f(ev)
	f(ev)
	ev.Child.Created = 10 // PID reused by a new process
	f(ev)
	if n != 2 {
		t.Fatalf("called %d times, want 2", n)
	}
}

// Needs administrator rights (true on GitHub's Windows runners).
func TestWatchETWSeesChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan StartEvent, 64)
	err := WatchETW(ctx, func(ev StartEvent) { got <- ev })
	if errors.Is(err, syscall.Errno(windows.ERROR_ACCESS_DENIED)) {
		t.Skip("not running as administrator")
	}
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // let the session start delivering
	cmd := exec.Command("cmd.exe", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(cmd.Process.Pid)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-got:
			if ev.Child.PID != pid {
				continue
			}
			if !strings.EqualFold(ev.Child.Name, "cmd.exe") || !strings.HasSuffix(strings.ToLower(ev.Child.Path), `\cmd.exe`) || ev.Child.Path[1] != ':' {
				t.Fatalf("child %+v", ev.Child)
			}
			if ev.Parent.Path == "" || !strings.HasSuffix(strings.ToLower(ev.Parent.Path), ".exe") {
				t.Fatalf("parent %+v", ev.Parent)
			}
			return
		case <-deadline:
			t.Fatalf("no ETW event for the child process within 10s; events=%d starts=%d noProps=%d noPath=%d noParent=%d emitted=%d traceEnded=%v traceResult=%d",
				etwStats.events.Load(), etwStats.starts.Load(), etwStats.noProps.Load(), etwStats.noPath.Load(), etwStats.noParent.Load(), etwStats.emitted.Load(), etwStats.traceEnded.Load(), etwStats.traceResult.Load())
		}
	}
}
