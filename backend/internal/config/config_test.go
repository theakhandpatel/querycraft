package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every test here controls the config file with t.Chdir, so no value leaks in
// from the developer's working directory or shell.

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, DefaultFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustLoad(t *testing.T, explicit string) Settings {
	t.Helper()
	s, err := Load(explicit)
	if err != nil {
		t.Fatalf("Load(%q): %v", explicit, err)
	}
	return s
}

func TestDefaultsAreShippedValues(t *testing.T) {
	s := Defaults()
	if s.Addr != "127.0.0.1:8080" {
		t.Errorf("Addr = %q, want the loopback default", s.Addr)
	}
	if s.MaxRows <= 0 || s.MaxBytes <= 0 || s.TimeoutMS <= 0 || s.MaxConcurrent <= 0 {
		t.Errorf("execution defaults are not positive: rows=%d bytes=%d timeout=%d concurrent=%d", s.MaxRows, s.MaxBytes, s.TimeoutMS, s.MaxConcurrent)
	}
	if s.AIProvider != "fake" {
		t.Errorf("AIProvider = %q, want fake (the lab must work with no key)", s.AIProvider)
	}
	if s.MaxEnvironments != 0 || s.IdleTTL != 0 {
		t.Errorf("environment knobs default to %d/%s, want 0/0 (the manager's own defaults)", s.MaxEnvironments, s.IdleTTL)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfig(t, dir, `{
		"addr": "127.0.0.1:9999",
		"max_rows": 7,
		"no_local_db": true,
		"sqlite_idle": "30m",
		"ai_provider": "off"
	}`)
	s := mustLoad(t, "")
	if s.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q, want the file's value", s.Addr)
	}
	if s.MaxRows != 7 {
		t.Errorf("MaxRows = %d, want 7", s.MaxRows)
	}
	if !s.NoLocalDB {
		t.Error("NoLocalDB = false, want the file's true")
	}
	if s.SQLiteIdle != 30*time.Minute {
		t.Errorf("SQLiteIdle = %s, want 30m", s.SQLiteIdle)
	}
	if s.AIProvider != "off" {
		t.Errorf("AIProvider = %q, want off", s.AIProvider)
	}
	// A key the file does not mention keeps the shipped default.
	if s.TimeoutMS != Defaults().TimeoutMS {
		t.Errorf("TimeoutMS = %d, want the untouched default %d", s.TimeoutMS, Defaults().TimeoutMS)
	}
}

func TestExplicitPathBeatsTheDefaultLocation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfig(t, dir, `{"max_rows": 11}`)
	elsewhere := filepath.Join(dir, "elsewhere.json")
	if err := os.WriteFile(elsewhere, []byte(`{"max_rows": 22}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := mustLoad(t, elsewhere)
	if s.MaxRows != 22 {
		t.Errorf("MaxRows = %d, want the --config file's 22 over the working-directory file's 11", s.MaxRows)
	}

	// A named file that does not exist is an error: someone asked for it.
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("Load with a missing explicit file returned no error")
	}
}

func TestDefaultLocationIsOptional(t *testing.T) {
	t.Chdir(t.TempDir()) // no querycraft.json here
	s := mustLoad(t, "")
	if s.Addr != Defaults().Addr {
		t.Errorf("Addr = %q, want the default with no file present", s.Addr)
	}
}

func TestUnknownKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfig(t, dir, `{"max_row": 5}`) // a typo for max_rows
	_, err := Load("")
	if err == nil {
		t.Fatal("a typo'd key loaded without error; it would have silently done nothing")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error %q does not name the problem", err)
	}
}

func TestWrongTypeIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfig(t, dir, `{"max_rows": "5"}`)
	if _, err := Load(""); err == nil {
		t.Fatal("a string where a number belongs loaded without error")
	}
	writeConfig(t, dir, `{"sqlite_idle": 15}`)
	if _, err := Load(""); err == nil {
		t.Fatal("a bare number duration loaded without error; its unit would be a guess")
	}
}

func TestInvalidJSONIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfig(t, dir, `{"max_rows":`)
	if _, err := Load(""); err == nil {
		t.Fatal("malformed JSON loaded without error")
	}
}

func TestDerivedPathsFollowPGData(t *testing.T) {
	s := Defaults()
	s.PGDataDir = filepath.Join(string(filepath.Separator), "srv", ".data", "pg", "data")
	if got := s.DeriveSQLiteRoot(); filepath.Base(got) != "sqlite" || filepath.Base(filepath.Dir(got)) != ".data" {
		t.Errorf("DeriveSQLiteRoot() = %q, want a sqlite root beside the pg data dir", got)
	}
	if got, want := s.DeriveMetadataPath(), filepath.Join(filepath.Dir(s.PGDataDir), "metadata.sqlite"); got != want {
		t.Errorf("DeriveMetadataPath() = %q, want %q", got, want)
	}
	// An explicit value is never overwritten by the derivation.
	s.SQLiteDataDir = "/tmp/opencode/own-sqlite"
	s.MetadataPath = "/tmp/opencode/own-metadata.sqlite"
	if s.DeriveSQLiteRoot() != s.SQLiteDataDir || s.DeriveMetadataPath() != s.MetadataPath {
		t.Error("an explicit sqlite/metadata path was replaced by the derived one")
	}
}
