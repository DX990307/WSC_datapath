package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFileExtendsAndComments(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "base.json", `{"bandwidth": 48, "timing": true, "_note": "x"}`)
	child := writeConfig(t, dir, "child.json",
		`{"extends": "base.json", "bandwidth": 24, "gpm-preset": "mi300x-eighth", "rate": 0.5}`)

	got, err := loadConfigFile(child, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"bandwidth": "24", "timing": "true", "gpm-preset": "mi300x-eighth", "rate": "0.5",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestLoadConfigFileRejectsCycle(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "a.json", `{"extends": "b.json"}`)
	writeConfig(t, dir, "b.json", `{"extends": "a.json"}`)
	if _, err := loadConfigFile(filepath.Join(dir, "a.json"), nil); err == nil {
		t.Fatal("extends cycle was not detected")
	}
}

// The repository's config files must load and name only real flags.
func TestRepositoryConfigsUseKnownFlags(t *testing.T) {
	paths, _ := filepath.Glob("../../configs/*.json")
	if len(paths) == 0 {
		t.Fatal("no config files found")
	}
	for _, p := range paths {
		values, err := loadConfigFile(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name := range values {
			if name == "benchmark" {
				continue // defined in package main
			}
			if lookupRunnerFlag(name) == nil {
				t.Errorf("%s: unknown flag %q", p, name)
			}
		}
	}
}
