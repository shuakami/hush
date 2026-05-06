package config

import (
	"path/filepath"
	"testing"
)

func TestLoadServerDefaultsToFileKEK(t *testing.T) {
	t.Setenv("HUSH_DATA_DIR", t.TempDir())
	t.Setenv("HUSH_KEK_KIND", "")
	t.Setenv("HUSH_KEK_FILE", "")
	t.Setenv("HUSH_KEK_ENV", "")

	cfg, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KEKKind != "file" {
		t.Fatalf("KEKKind = %q, want file", cfg.KEKKind)
	}
	want := filepath.Join(cfg.DataDir, "master.key")
	if cfg.KEKValue != want {
		t.Fatalf("KEKValue = %q, want %q", cfg.KEKValue, want)
	}
}

func TestLoadServerCanUseEnvKEK(t *testing.T) {
	t.Setenv("HUSH_DATA_DIR", t.TempDir())
	t.Setenv("HUSH_KEK_KIND", "env")
	t.Setenv("HUSH_KEK_ENV", "CUSTOM_HUSH_KEY")

	cfg, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KEKKind != "env" {
		t.Fatalf("KEKKind = %q, want env", cfg.KEKKind)
	}
	if cfg.KEKValue != "CUSTOM_HUSH_KEY" {
		t.Fatalf("KEKValue = %q, want CUSTOM_HUSH_KEY", cfg.KEKValue)
	}
}
