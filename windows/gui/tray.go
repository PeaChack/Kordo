//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	goruntime "runtime"
	"sync"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/PeaChack/Kordo/core/rpcapi"
)

// Tray: a notification-area icon showing whether the VPN is up, with a menu
// to open the window, connect/disconnect and quit. Closing the window only
// hides it (options.App.HideWindowOnClose); "Выход" quits.
type tray struct {
	app *App

	mu        sync.Mutex
	ready     bool
	state     string
	online    bool
	iconOn    []byte
	iconOff   []byte
	toggle    *systray.MenuItem
	lastIcon  *[]byte
	lastTitle string
}

var stateText = map[string]string{
	"off":        "отключено",
	"connecting": "подключение…",
	"connected":  "подключено",
	"degraded":   "сервер недоступен",
	"blocked":    "заблокировано kill switch",
	"retrying":   "переподключение…",
}

func (a *App) startTray(assets fs.FS) {
	t := &tray{app: a}
	if logo, err := fs.ReadFile(assets, "logo.png"); err == nil {
		t.iconOn, t.iconOff = trayIcons(logo)
	}
	a.tray = t
	go func() {
		// the tray's window and message loop live on this thread
		goruntime.LockOSThread()
		systray.Run(t.onReady, nil)
	}()
}

func (t *tray) onReady() {
	systray.SetTooltip("Kordo")
	systray.SetOnClick(func(systray.IMenu) { t.app.ShowWindow() })
	open := systray.AddMenuItem("Открыть Kordo", "")
	open.Click(func() { t.app.ShowWindow() })
	t.toggle = systray.AddMenuItem("Подключить", "")
	t.toggle.Click(t.onToggle)
	systray.AddSeparator()
	quit := systray.AddMenuItem("Выход", "Закрыть окно Kordo (служба и VPN продолжат работать)")
	quit.Click(func() { runtime.Quit(t.app.ctx) })
	t.mu.Lock()
	t.ready = true
	t.mu.Unlock()
	t.refresh()
}

func (t *tray) onToggle() {
	t.mu.Lock()
	st := t.state
	t.mu.Unlock()
	method := rpcapi.MEngineConnect
	if st != "" && st != "off" {
		method = rpcapi.MEngineDisconn
	}
	go func() { _, _ = t.app.Call(method, "") }()
}

// onStatus takes a state.changed notification or an engine.status result.
func (t *tray) onStatus(raw []byte) {
	var n struct {
		State  string `json:"state"`
		Status *struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if json.Unmarshal(raw, &n) != nil {
		return
	}
	st := n.State
	if n.Status != nil {
		st = n.Status.State
	}
	if st == "" {
		return
	}
	t.mu.Lock()
	t.state = st
	t.mu.Unlock()
	t.refresh()
}

func (t *tray) setOnline(v bool) {
	t.mu.Lock()
	t.online = v
	if !v {
		t.state = ""
	}
	t.mu.Unlock()
	if v {
		go func() {
			if raw, err := t.app.Call(rpcapi.MEngineStatus, ""); err == nil {
				t.onStatus([]byte(raw))
			}
		}()
	}
	t.refresh()
}

func (t *tray) refresh() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ready {
		return
	}
	tip := "Kordo — служба недоступна"
	icon := &t.iconOff
	toggle := "Подключить"
	if t.online {
		tip = "Kordo — " + stateText["off"]
		if s, ok := stateText[t.state]; ok {
			tip = "Kordo — " + s
		}
		if t.state != "" && t.state != "off" {
			toggle = "Отключить"
		}
		if t.state == "connected" || t.state == "degraded" {
			icon = &t.iconOn
		}
	}
	if t.toggle != nil {
		if t.online {
			t.toggle.Enable()
		} else {
			t.toggle.Disable()
		}
		if toggle != t.lastTitle {
			t.toggle.SetTitle(toggle)
			t.lastTitle = toggle
		}
	}
	systray.SetTooltip(tip)
	if len(*icon) > 0 && icon != t.lastIcon {
		systray.SetIcon(*icon)
		t.lastIcon = icon
	}
}

func (t *tray) quit() { systray.Quit() }

// trayIcons builds .ico files (PNG entries 16..48 px) from the logo: the
// original for "connected" and a faded grey one for everything else.
func trayIcons(logo []byte) (on, off []byte) {
	src, err := png.Decode(bytes.NewReader(logo))
	if err != nil {
		return nil, nil
	}
	sizes := []int{16, 24, 32, 48}
	var onImgs, offImgs []image.Image
	for _, n := range sizes {
		img := scale(src, n)
		onImgs = append(onImgs, img)
		offImgs = append(offImgs, grey(img))
	}
	return ico(onImgs), ico(offImgs)
}

// scale downsizes with a box filter (area average).
func scale(src image.Image, n int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/n, b.Min.Y+(y+1)*b.Dy()/n
		for x := 0; x < n; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/n, b.Min.X+(x+1)*b.Dx()/n
			var r, g, bl, a, cnt uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBA64Model.Convert(src.At(sx, sy)).(color.NRGBA64)
					// premultiply so transparent pixels do not darken edges
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					cnt++
				}
			}
			if cnt == 0 || a == 0 {
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r / a >> 8), G: uint8(g / a >> 8), B: uint8(bl / a >> 8),
				A: uint8(a / cnt >> 8),
			})
		}
	}
	return dst
}

func grey(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Rect)
	for i := 0; i+3 < len(src.Pix); i += 4 {
		r, g, b, a := src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3]
		l := uint8((299*uint32(r) + 587*uint32(g) + 114*uint32(b)) / 1000)
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2] = l, l, l
		dst.Pix[i+3] = uint8(uint32(a) * 3 / 4)
	}
	return dst
}

// ico packs images as PNG-compressed icon entries (valid since Windows Vista).
func ico(imgs []image.Image) []byte {
	var pngs [][]byte
	for _, img := range imgs {
		var b bytes.Buffer
		if png.Encode(&b, img) != nil {
			return nil
		}
		pngs = append(pngs, b.Bytes())
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(pngs))})
	offset := 6 + 16*len(pngs)
	for i, p := range pngs {
		n := imgs[i].Bounds().Dx()
		_ = binary.Write(&out, le, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{uint8(n), uint8(n), 0, 0, 1, 32, uint32(len(p)), uint32(offset)})
		offset += len(p)
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes()
}
