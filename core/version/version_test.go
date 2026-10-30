package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.1", "0.2.1", 0},
		{"v0.2.1", "0.2.1", 0},
		{"0.2.1", "0.2.1.0", 0},
		{"0.2.1+abc", "0.2.1", 0},
		{"0.2.1", "0.2.2", -1},
		{"0.10.0", "0.9.9", 1},
		{"1.0", "0.99.99", 1},
		{"0.2.1-dev.3", "0.2.1", -1},
		{"0.2.1-dev.3", "0.2.0", 1},
		{"0.2.1-dev.3", "0.2.1-dev.10", -1},
		{"0.3.0-rc.1", "0.3.0-rc.2", -1},
		{"0.3.0-rc.1", "0.3.0-beta", 1},
		{"0.3.0-1", "0.3.0-rc", -1},
		{"0.3.0-rc", "0.3.0-rc.1", -1},
		{"dev", "0.0.1", -1},
		{"dev", "", 0},
		{"0.1", "garbage", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, s := range []string{"0.2.1", "v1.2.3.4", "0.2.1-dev.3", "1-rc"} {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false", s)
		}
	}
	for _, s := range []string{"", "dev", "1.2.3.4.5", "1.x", "1.2-", "-1.2"} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true", s)
		}
	}
}
