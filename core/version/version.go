// Package version compares Kordo versions: a.b.c[.d][-prerelease][+build],
// e.g. "0.2.1", "v0.3.0-rc.1", "0.2.1-dev.3". A release is newer than any of
// its prereleases (0.2.1-dev.3 < 0.2.1), as in semantic versioning.
package version

import (
	"strconv"
	"strings"
)

type parsed struct {
	nums []int
	pre  []string
}

func parse(s string) (parsed, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var p parsed
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		if s[i+1:] == "" {
			return p, false
		}
		p.pre = strings.Split(s[i+1:], ".")
	}
	for _, f := range strings.Split(core, ".") {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return p, false
		}
		p.nums = append(p.nums, n)
	}
	if len(p.nums) == 0 || len(p.nums) > 4 {
		return p, false
	}
	return p, true
}

// Valid reports whether s is a version Compare understands.
func Valid(s string) bool {
	_, ok := parse(s)
	return ok
}

// Compare returns -1, 0 or 1 when a is older than, equal to or newer than b.
// Invalid versions are older than valid ones.
func Compare(a, b string) int {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 4; i++ {
		x, y := at(pa.nums, i), at(pb.nums, i)
		if x != y {
			return sign(x - y)
		}
	}
	// no prerelease > prerelease
	switch {
	case len(pa.pre) == 0 && len(pb.pre) == 0:
		return 0
	case len(pa.pre) == 0:
		return 1
	case len(pb.pre) == 0:
		return -1
	}
	for i := 0; i < len(pa.pre) && i < len(pb.pre); i++ {
		if c := comparePre(pa.pre[i], pb.pre[i]); c != 0 {
			return c
		}
	}
	return sign(len(pa.pre) - len(pb.pre))
}

// comparePre: numeric identifiers compare numerically and are lower than
// alphanumeric ones, which compare as strings.
func comparePre(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return sign(na - nb)
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func at(n []int, i int) int {
	if i < len(n) {
		return n[i]
	}
	return 0
}

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	}
	return 0
}
