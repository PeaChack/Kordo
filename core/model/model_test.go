package model

import "testing"

func TestEffectiveMTU(t *testing.T) {
	for in, want := range map[int]int{0: DefaultMTU, 1500: 1500, 1280: 1280, 576: 1280, 65535: 9000, 9000: 9000} {
		if got := (Profile{MTU: in}).EffectiveMTU(); got != want {
			t.Errorf("MTU %d -> %d, want %d", in, got, want)
		}
	}
}
