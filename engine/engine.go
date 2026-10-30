// Package engine embeds sing-box as a library. It never runs an external
// binary. Build with the tags from build/tags.txt (with_gvisor, with_wireguard,
// with_utls, with_clash_api), otherwise WireGuard/Reality are unavailable.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func baseContext() context.Context {
	return include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
}

func parse(ctx context.Context, config []byte) (option.Options, error) {
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, config)
	if err != nil {
		return option.Options{}, fmt.Errorf("decode sing-box config: %w", err)
	}
	return opts, nil
}

// Check validates a configuration the same way `sing-box check` does:
// strict JSON decoding plus constructing (but not starting) the instance.
func Check(config []byte) error {
	ctx, cancel := context.WithCancel(baseContext())
	defer cancel()
	opts, err := parse(ctx, config)
	if err != nil {
		return err
	}
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return fmt.Errorf("sing-box rejected config: %w", err)
	}
	return inst.Close()
}

// Instance is a running sing-box.
type Instance struct {
	ctx         context.Context
	cancel      context.CancelFunc
	box         *box.Box
	clashAddr   string
	clashSecret string
	closeOnce   sync.Once
	closeErr    error
}

type Options struct {
	ClashAPI    string // must match the config's experimental.clash_api
	ClashSecret string
}

func Start(config []byte, o Options) (*Instance, error) {
	ctx, cancel := context.WithCancel(baseContext())
	opts, err := parse(ctx, config)
	if err != nil {
		cancel()
		return nil, err
	}
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create engine: %w", err)
	}
	if err := b.Start(); err != nil {
		_ = b.Close()
		cancel()
		return nil, fmt.Errorf("start engine: %w", err)
	}
	return &Instance{ctx: ctx, cancel: cancel, box: b, clashAddr: o.ClashAPI, clashSecret: o.ClashSecret}, nil
}

func (i *Instance) Close() error {
	i.closeOnce.Do(func() {
		done := make(chan error, 1)
		go func() { done <- i.box.Close() }()
		select {
		case i.closeErr = <-done:
		case <-time.After(15 * time.Second):
			i.closeErr = errors.New("engine close timed out")
		}
		i.cancel()
	})
	return i.closeErr
}

func (i *Instance) dialer(tag string) (N.Dialer, error) {
	if ob, ok := i.box.Outbound().Outbound(tag); ok {
		return ob, nil
	}
	if ep, ok := i.box.Endpoint().Get(tag); ok {
		return ep, nil
	}
	return nil, fmt.Errorf("outbound %q not found", tag)
}

// URLTest measures an HTTP round trip through the given outbound/endpoint.
func (i *Instance) URLTest(ctx context.Context, tag, link string) (time.Duration, error) {
	d, err := i.dialer(tag)
	if err != nil {
		return 0, err
	}
	ms, err := urltest.URLTest(ctx, link, d)
	if err != nil {
		return 0, err
	}
	return time.Duration(ms) * time.Millisecond, nil
}

// ResetNetwork tells the engine the network changed: drops cached interface
// state, closes idle connections and resets DNS caches.
func (i *Instance) ResetNetwork() {
	i.box.Router().ResetNetwork()
	i.box.CloseIdleConnections()
}

// ---- Clash API helpers (connections view / closing connections)

type Connection struct {
	ID       string   `json:"id"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Start    string   `json:"start"`
	Chains   []string `json:"chains"`
	Rule     string   `json:"rule"`
	Metadata struct {
		Network     string `json:"network"`
		Host        string `json:"host"`
		DstIP       string `json:"destinationIP"`
		DstPort     string `json:"destinationPort"`
		ProcessPath string `json:"processPath"`
	} `json:"metadata"`
}

func (i *Instance) clash(ctx context.Context, method, path string) ([]byte, error) {
	if i.clashAddr == "" {
		return nil, errors.New("clash api disabled")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+i.clashAddr+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+i.clashSecret)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("clash api %s %s: %s", method, path, resp.Status)
	}
	return body, err
}

func (i *Instance) Connections(ctx context.Context) ([]Connection, error) {
	body, err := i.clash(ctx, http.MethodGet, "/connections")
	if err != nil {
		return nil, err
	}
	var v struct {
		Connections []Connection `json:"connections"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return v.Connections, nil
}

func (i *Instance) CloseAllConnections(ctx context.Context) error {
	_, err := i.clash(ctx, http.MethodDelete, "/connections")
	return err
}
