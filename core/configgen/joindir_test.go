package configgen

import "testing"

func TestJoinDir(t *testing.T) {
	cases := [][3]string{
		{`C:\ProgramData\Kordo\rules`, "a.json", `C:\ProgramData\Kordo\rules\a.json`},
		{`C:\ProgramData\Kordo\rules\`, "a.json", `C:\ProgramData\Kordo\rules\a.json`},
		{"/data/user/0/kordo/rules", "a.json", "/data/user/0/kordo/rules/a.json"},
		{"/data/rules/", "a.json", "/data/rules/a.json"},
	}
	for _, c := range cases {
		if got := joinDir(c[0], c[1]); got != c[2] {
			t.Errorf("joinDir(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
