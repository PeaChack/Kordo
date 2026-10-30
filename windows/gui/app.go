//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows/registry"

	"github.com/PeaChack/Kordo/core/rpcapi"
)

// App is bound to the frontend. All service calls go through Call, which
// keeps a single pipe connection and reconnects transparently.
type App struct {
	ctx context.Context

	mu     sync.Mutex
	client *rpcapi.Client
	online bool
	stop   chan struct{}
}

func NewApp() *App { return &App{stop: make(chan struct{})} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startBridge()
	runtime.OnFileDrop(ctx, func(x, y int, paths []string) {
		runtime.EventsEmit(ctx, "files:dropped", a.resolvePaths(paths))
	})
	go a.connectLoop()
}

func (a *App) shutdown(ctx context.Context) {
	close(a.stop)
	a.mu.Lock()
	if a.client != nil {
		a.client.Close()
	}
	a.mu.Unlock()
}

func (a *App) onSecondInstance(_ options.SecondInstanceData) {
	runtime.WindowUnminimise(a.ctx)
	runtime.Show(a.ctx)
}

func (a *App) setOnline(v bool) {
	a.mu.Lock()
	changed := a.online != v
	a.online = v
	a.mu.Unlock()
	if changed {
		runtime.EventsEmit(a.ctx, "service:online", v)
	}
}

func (a *App) connectLoop() {
	for {
		select {
		case <-a.stop:
			return
		default:
		}
		timeout := 2 * time.Second
		conn, err := winio.DialPipe(rpcapi.PipeName, &timeout)
		if err != nil {
			a.setOnline(false)
			select {
			case <-a.stop:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		c := rpcapi.NewClient(conn, func(method string, params json.RawMessage) {
			runtime.EventsEmit(a.ctx, "rpc:"+method, string(params))
		})
		a.mu.Lock()
		a.client = c
		a.mu.Unlock()
		a.setOnline(true)
		select {
		case <-c.Done():
		case <-a.stop:
			return
		}
		a.mu.Lock()
		a.client = nil
		a.mu.Unlock()
		a.setOnline(false)
	}
}

// ServiceOnline reports whether the GUI is connected to the service.
func (a *App) ServiceOnline() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.online
}

// Call invokes a service method. params and the result are JSON strings.
func (a *App) Call(method string, params string) (string, error) {
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c == nil {
		return "", errors.New("служба Kordo недоступна")
	}
	var p any
	if params != "" {
		p = json.RawMessage(params)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	raw, err := c.CallRaw(ctx, method, p)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// PickExecutable opens a file dialog for .exe/.lnk files and returns the resolved exe paths.
func (a *App) PickExecutable() ([]string, error) {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Выберите приложение",
		Filters: []runtime.FileFilter{
			{DisplayName: "Приложения (*.exe, *.lnk)", Pattern: "*.exe;*.lnk"},
		},
	})
	if err != nil {
		return nil, err
	}
	return a.resolvePaths(paths), nil
}

func (a *App) resolvePaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".lnk":
			if t := resolveShortcut(p); t != "" && strings.EqualFold(filepath.Ext(t), ".exe") {
				out = append(out, t)
			}
		case ".exe":
			out = append(out, p)
		}
	}
	return out
}

// resolveShortcut returns the target of a .lnk via the WScript.Shell COM object.
func resolveShortcut(lnk string) string {
	script := "(New-Object -ComObject WScript.Shell).CreateShortcut($args[0]).TargetPath"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, lnk)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Icon returns a data: URL with the exe icon (cached).
func (a *App) Icon(path string) string { return iconDataURL(path) }

// Icons resolves many icons at once.
func (a *App) Icons(paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		out[p] = iconDataURL(p)
	}
	return out
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func (a *App) GetAutostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Kordo")
	return err == nil
}

func (a *App) SetAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	_ = k.DeleteValue("Portico") // entry left by the app before its rename
	if !on {
		err := k.DeleteValue("Kordo")
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("Kordo", `"`+exe+`" --minimized`)
}

// SaveText lets the user save diagnostics to a file.
func (a *App) SaveText(defaultName, text string) error {
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{DefaultFilename: defaultName, Title: "Сохранить"})
	if err != nil || p == "" {
		return err
	}
	return os.WriteFile(p, []byte(text), 0o644)
}

// Notify shows a system notification (best effort).
func (a *App) Notify(title, body string) {
	if err := runtime.InitializeNotifications(a.ctx); err != nil {
		return
	}
	_ = runtime.SendNotification(a.ctx, runtime.NotificationOptions{ID: title + body, Title: title, Body: body})
}

// ShowWindow brings the window to front.
func (a *App) ShowWindow() {
	runtime.WindowUnminimise(a.ctx)
	runtime.Show(a.ctx)
}
