package wf

import (
	"testing"
	"unsafe"
)

// Regression test for the Kordo patch: allocations after an odd-sized blob
// must stay pointer-aligned, otherwise writing a struct with pointers into
// them aborts the Go 1.26 runtime.
func TestArenaAlignment(t *testing.T) {
	var a arena
	defer a.Dispose()
	for _, n := range []uintptr{1, 3, 7, 13, 21, 2, 9} {
		_ = a.Alloc(n)
		p := uintptr(a.Alloc(unsafe.Sizeof(fwpmFilter0{})))
		if p%unsafe.Sizeof(uintptr(0)) != 0 {
			t.Fatalf("allocation after %d-byte blob is misaligned: %#x", n, p)
		}
		f := (*fwpmFilter0)(unsafe.Pointer(p))
		*f = fwpmFilter0{DisplayData: fwpmDisplayData0{Name: toUint16(&a, "x")}}
	}
}
