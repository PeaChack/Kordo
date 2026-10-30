package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PeaChack/Kordo/core/configgen"
	"github.com/PeaChack/Kordo/core/configgen/cgtest"
	"github.com/PeaChack/Kordo/engine"
)

// TestGeneratedConfigsAreAccepted is the equivalent of `sing-box check` on
// every configgen fixture: strict decoding and instance construction.
func TestGeneratedConfigsAreAccepted(t *testing.T) {
	for name, f := range cgtest.Fixtures(t) {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			f.Env.RulesDir = dir
			out, err := configgen.Generate(f.Profile, f.Servers, f.Env)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range out.RuleSets {
				if err := os.WriteFile(filepath.Join(dir, k), v, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := engine.Check(out.Config); err != nil {
				t.Fatalf("%v\n%s", err, out.Config)
			}
		})
	}
}
