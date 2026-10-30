//go:build windows

package main

import (
	"encoding/json"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The frontend talks to Go through events instead of generated bindings:
// it emits "bridge:call" {id, fn, args} and receives "bridge:reply"
// {id, result, error}. This keeps the build free of the Wails CLI.
type bridgeCall struct {
	ID   int64             `json:"id"`
	Fn   string            `json:"fn"`
	Args []json.RawMessage `json:"args"`
}

type bridgeReply struct {
	ID     int64  `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func arg[T any](c bridgeCall, i int) T {
	var v T
	if i < len(c.Args) {
		_ = json.Unmarshal(c.Args[i], &v)
	}
	return v
}

func (a *App) startBridge() {
	runtime.EventsOn(a.ctx, "bridge:call", func(data ...interface{}) {
		if len(data) == 0 {
			return
		}
		s, ok := data[0].(string)
		if !ok {
			return
		}
		var c bridgeCall
		if err := json.Unmarshal([]byte(s), &c); err != nil {
			return
		}
		go func() {
			res, err := a.dispatch(c)
			r := bridgeReply{ID: c.ID, Result: res}
			if err != nil {
				r.Error = err.Error()
			}
			b, _ := json.Marshal(r)
			runtime.EventsEmit(a.ctx, "bridge:reply", string(b))
		}()
	})
}

func (a *App) dispatch(c bridgeCall) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error: %v", p)
		}
	}()
	switch c.Fn {
	case "Call":
		return a.Call(arg[string](c, 0), arg[string](c, 1))
	case "ServiceOnline":
		return a.ServiceOnline(), nil
	case "PickExecutable":
		return a.PickExecutable()
	case "Icons":
		return a.Icons(arg[[]string](c, 0)), nil
	case "GetAutostart":
		return a.GetAutostart(), nil
	case "SetAutostart":
		return nil, a.SetAutostart(arg[bool](c, 0))
	case "SaveText":
		return nil, a.SaveText(arg[string](c, 0), arg[string](c, 1))
	case "Notify":
		a.Notify(arg[string](c, 0), arg[string](c, 1))
		return nil, nil
	case "Version":
		return a.Version(), nil
	case "CheckUpdate":
		return a.CheckUpdate()
	case "InstallUpdate":
		return nil, a.InstallUpdate()
	case "OpenReleasePage":
		a.OpenReleasePage(arg[string](c, 0))
		return nil, nil
	case "GetUpdateCheck":
		return a.GetUpdateCheck(), nil
	case "SetUpdateCheck":
		return nil, a.SetUpdateCheck(arg[bool](c, 0))
	case "ShowWindow":
		a.ShowWindow()
		return nil, nil
	}
	return nil, fmt.Errorf("unknown function %q", c.Fn)
}
