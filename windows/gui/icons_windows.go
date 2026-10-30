//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	user32             = windows.NewLazySystemDLL("user32.dll")
	gdi32              = windows.NewLazySystemDLL("gdi32.dll")
	procSHGetFileInfoW = shell32.NewProc("SHGetFileInfoW")
	procGetIconInfo    = user32.NewProc("GetIconInfo")
	procDestroyIcon    = user32.NewProc("DestroyIcon")
	procGetDC          = user32.NewProc("GetDC")
	procReleaseDC      = user32.NewProc("ReleaseDC")
	procGetDIBits      = gdi32.NewProc("GetDIBits")
	procDeleteObject   = gdi32.NewProc("DeleteObject")
	procGetObjectW     = gdi32.NewProc("GetObjectW")

	iconCache sync.Map // path -> data URL ("" if none)
)

const (
	shgfiIcon      = 0x000000100
	shgfiLargeIcon = 0x000000000
)

type shFileInfo struct {
	hIcon         windows.Handle
	iIcon         int32
	dwAttributes  uint32
	szDisplayName [260]uint16
	szTypeName    [80]uint16
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type bitmap struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

func iconDataURL(path string) string {
	if v, ok := iconCache.Load(path); ok {
		return v.(string)
	}
	url := ""
	if img := exeIcon(path); img != nil {
		var buf bytes.Buffer
		if png.Encode(&buf, img) == nil {
			url = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	iconCache.Store(path, url)
	return url
}

func exeIcon(path string) image.Image {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var fi shFileInfo
	r, _, _ := procSHGetFileInfoW.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&fi)), unsafe.Sizeof(fi), shgfiIcon|shgfiLargeIcon)
	if r == 0 || fi.hIcon == 0 {
		return nil
	}
	defer procDestroyIcon.Call(uintptr(fi.hIcon))
	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(uintptr(fi.hIcon), uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil
	}
	defer procDeleteObject.Call(uintptr(ii.hbmColor))
	defer procDeleteObject.Call(uintptr(ii.hbmMask))
	if ii.hbmColor == 0 {
		return nil
	}
	var bm bitmap
	procGetObjectW.Call(uintptr(ii.hbmColor), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if w <= 0 || h <= 0 || w > 512 || h > 512 {
		return nil
	}
	bih := bitmapInfoHeader{biSize: uint32(unsafe.Sizeof(bitmapInfoHeader{})), biWidth: int32(w), biHeight: -int32(h), biPlanes: 1, biBitCount: 32}
	pix := make([]byte, w*h*4)
	dc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, dc)
	if r, _, _ := procGetDIBits.Call(dc, uintptr(ii.hbmColor), 0, uintptr(h), uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&bih)), 0); r == 0 {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	hasAlpha := false
	for i := 0; i < w*h; i++ {
		if pix[i*4+3] != 0 {
			hasAlpha = true
			break
		}
	}
	for i := 0; i < w*h; i++ {
		b, g, r, a := pix[i*4], pix[i*4+1], pix[i*4+2], pix[i*4+3]
		if !hasAlpha {
			a = 255
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = r, g, b, a
	}
	return img
}
