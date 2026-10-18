// Package config resolves the gateway's settings. Every setting starts from a
// shipped default, then the config file may override it. cmd/api applies the
// one command-line layer, --addr, on top. Each layer overrides the previous
// one, and a layer that says nothing about a key leaves the earlier value
// standing.
//
// The config file is JSON, named querycraft.json. It is looked up in this
// order: the --config flag, then ./querycraft.json in the working directory.
// A named file that does not exist is an error, because someone asked for it
// by name; the default ./ location is optional and a missing file there is not
// an error.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/httpapi"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
)

// DefaultFileName is the config file read from the working directory when no
// more specific location was named.
const DefaultFileName = "querycraft.json"

// Settings is the gateway's whole configuration surface. Field comments name
// the JSON key that sets each one. Empty strings and zero durations mean "not
// set", which at use time derives from another setting or keeps the consuming
// package's own default.
type Settings struct {
	// Server.
	Addr             string // "addr": listen address. Loopback by default, which is what allows the seeded administrator to keep an empty password.
	MaxRows          int    // "max_rows"
	MaxBytes         int    // "max_bytes"
	TimeoutMS        int    // "timeout_ms"
	MaxConcurrent    int    // "max_concurrent"
	LoginMaxFailures int    // "login_max_failures"
	LoginWindowMS    int    // "login_window_ms"
	LoginMaxTracked  int    // "login_max_tracked"

	// Databases. DatabaseURL, LearnerDatabaseURL and AdminDatabaseURL are DSNs;
	// PGPassword and AdminPassword are secrets. None of them is ever logged.
	DatabaseURL        string        // "database_url": learner execution DSN; empty starts the local server
	NoLocalDB          bool          // "no_local_db": never start the local server
	PGDataDir          string        // "pg_data": cluster data directory for the local server
	PGRuntimeDir       string        // "pg_runtime"
	PGBinariesDir      string        // "pg_binaries": prebuilt-binary download cache
	PGPort             int           // "pg_port"
	PGUser             string        // "pg_user"
	PGPassword         string        // "pg_password"
	PGDatabase         string        // "pg_database"
	SQLiteDataDir      string        // "sqlite_data": empty derives from PGDataDir
	SQLiteMaxAccounts  int           // "sqlite_max_accounts": 0 keeps the store's own default
	SQLiteIdle         time.Duration // "sqlite_idle": 0 keeps the store's own default
	LearnerDatabaseURL string        // "learner_database_url": empty derives from DatabaseURL
	AdminDatabaseURL   string        // "admin_database_url": metadata DSN; empty falls back to DatabaseURL
	Metadata           string        // "metadata": "sqlite" selects the SQLite metadata store; empty keeps PostgreSQL selection
	MetadataPath       string        // "metadata_path": empty derives from PGDataDir

	// The seeded default administrator.
	AdminEmail    string // "admin_email"
	AdminPassword string // "admin_password": empty means a blank-password admin, which is refused on a non-loopback Addr
	AdminName     string // "admin_name"

	// Learner challenge environments (ADR 0003/0006). The PostgreSQL manager
	// reads these the same way the SQLite store reads its own bounds.
	MaxEnvironments int           // "max_environments": 0 keeps envdb's default (8)
	IdleTTL         time.Duration // "idle_ttl": 0 keeps envdb's default (15m)

	// The challenge-lab AI provider (ADR 0005).
	AIProvider       string // "ai_provider": "fake", "openai-compatible", or an off value
	AIBaseURL        string // "ai_base_url"
	AIModel          string // "ai_model"
	AIKey            string // "ai_key"
	AITimeoutMS      int    // "ai_timeout_ms"
	AIMaxOutputBytes int    // "ai_max_output_bytes"
	AIRunTimeoutMS   int    // "ai_run_timeout_ms": 0 derives the run budget from AITimeoutMS
}

// Defaults returns the shipped settings: the values an install that sets
// nothing gets. The defaults that live in other packages are read from those
// packages, so the two can never disagree.
func Defaults() Settings {
	return Settings{
		Addr:             "127.0.0.1:8080",
		MaxRows:          engine.DefaultMaxRows,
		MaxBytes:         engine.DefaultMaxBytes,
		TimeoutMS:        engine.DefaultTimeoutMS,
		MaxConcurrent:    4,
		LoginMaxFailures: httpapi.DefaultLoginMaxFailures,
		LoginWindowMS:    httpapi.DefaultLoginWindowMS,
		LoginMaxTracked:  httpapi.DefaultLoginMaxTracked,
		PGDataDir:        localdb.DefaultDataDir() + "/data",
		PGRuntimeDir:     localdb.DefaultDataDir() + "/runtime",
		PGBinariesDir:    localdb.BinariesDir(),
		PGPort:           54329,
		PGUser:           "querycraft",
		PGPassword:       "querycraft",
		PGDatabase:       "querycraft",
		AdminEmail:       "admin@querycraft.local",
		AdminName:        "Administrator",
		AIProvider:       "fake",
		// Sized for the largest legitimate answer, not the smallest: a course
		// bundle is four chapters plus every exercise in one reply, and a
		// hosted model needed 58.5 s for one. The pipeline's run budget is
		// derived from this value, so the two can never disagree.
		AITimeoutMS:      120000,
		AIMaxOutputBytes: provider.DefaultMaxOutputBytes,
	}
}

// Load resolves the settings: defaults, then the config file. The command-line
// flag layer is applied by the caller (see applyAddrFlag in cmd/api), because
// the flags a subcommand accepts are the CLI's business, not this package's.
func Load(explicitPath string) (Settings, error) {
	s := Defaults()
	path, err := ResolveFile(explicitPath)
	if err != nil {
		return Settings{}, err
	}
	if path != "" {
		if err := readFile(&s, path); err != nil {
			return Settings{}, err
		}
	}
	return s, nil
}

// ResolveFile decides which config file to read, without reading it: an
// explicitly named path (--config) is returned as
// asked for and must exist, while ./querycraft.json is returned only when it
// exists. An empty result means no file.
func ResolveFile(explicitPath string) (string, error) {
	if explicitPath != "" {
		named := explicitPath
		if _, err := os.Stat(named); err != nil {
			return "", fmt.Errorf("config file %s: %w", named, err)
		}
		return named, nil
	}
	if _, err := os.Stat(DefaultFileName); err == nil {
		return DefaultFileName, nil
	}
	return "", nil
}

// readFile decodes one config file onto s. Unknown keys are refused: a typo
// such as "max_row" must fail loudly rather than silently leave the shipped
// default in place.
func readFile(s *Settings, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for key, value := range keys {
		if err := s.applyKey(key, value); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// applyKey sets one file key. Each accepted key is listed here, so this switch
// is the file schema.
func (s *Settings) applyKey(key string, value json.RawMessage) error {
	switch key {
	case "addr":
		return unmarshalInto(value, &s.Addr)
	case "max_rows":
		return unmarshalInto(value, &s.MaxRows)
	case "max_bytes":
		return unmarshalInto(value, &s.MaxBytes)
	case "timeout_ms":
		return unmarshalInto(value, &s.TimeoutMS)
	case "max_concurrent":
		return unmarshalInto(value, &s.MaxConcurrent)
	case "login_max_failures":
		return unmarshalInto(value, &s.LoginMaxFailures)
	case "login_window_ms":
		return unmarshalInto(value, &s.LoginWindowMS)
	case "login_max_tracked":
		return unmarshalInto(value, &s.LoginMaxTracked)
	case "database_url":
		return unmarshalInto(value, &s.DatabaseURL)
	case "no_local_db":
		return unmarshalInto(value, &s.NoLocalDB)
	case "pg_data":
		return unmarshalInto(value, &s.PGDataDir)
	case "pg_runtime":
		return unmarshalInto(value, &s.PGRuntimeDir)
	case "pg_binaries":
		return unmarshalInto(value, &s.PGBinariesDir)
	case "pg_port":
		return unmarshalInto(value, &s.PGPort)
	case "pg_user":
		return unmarshalInto(value, &s.PGUser)
	case "pg_password":
		return unmarshalInto(value, &s.PGPassword)
	case "pg_database":
		return unmarshalInto(value, &s.PGDatabase)
	case "sqlite_data":
		return unmarshalInto(value, &s.SQLiteDataDir)
	case "sqlite_max_accounts":
		return unmarshalInto(value, &s.SQLiteMaxAccounts)
	case "sqlite_idle":
		return unmarshalDuration(value, &s.SQLiteIdle)
	case "learner_database_url":
		return unmarshalInto(value, &s.LearnerDatabaseURL)
	case "admin_database_url":
		return unmarshalInto(value, &s.AdminDatabaseURL)
	case "metadata":
		return unmarshalInto(value, &s.Metadata)
	case "metadata_path":
		return unmarshalInto(value, &s.MetadataPath)
	case "admin_email":
		return unmarshalInto(value, &s.AdminEmail)
	case "admin_password":
		return unmarshalInto(value, &s.AdminPassword)
	case "admin_name":
		return unmarshalInto(value, &s.AdminName)
	case "max_environments":
		return unmarshalInto(value, &s.MaxEnvironments)
	case "idle_ttl":
		return unmarshalDuration(value, &s.IdleTTL)
	case "ai_provider":
		return unmarshalInto(value, &s.AIProvider)
	case "ai_base_url":
		return unmarshalInto(value, &s.AIBaseURL)
	case "ai_model":
		return unmarshalInto(value, &s.AIModel)
	case "ai_key":
		return unmarshalInto(value, &s.AIKey)
	case "ai_timeout_ms":
		return unmarshalInto(value, &s.AITimeoutMS)
	case "ai_max_output_bytes":
		return unmarshalInto(value, &s.AIMaxOutputBytes)
	case "ai_run_timeout_ms":
		return unmarshalInto(value, &s.AIRunTimeoutMS)
	default:
		return fmt.Errorf("unknown key %q", key)
	}
}

// unmarshalInto decodes one JSON value into target. It does not name the key in
// the error because applyKey already wraps the result with the file path, and
// the JSON error itself carries the type mismatch.
func unmarshalInto(value json.RawMessage, target any) error {
	if err := json.Unmarshal(value, target); err != nil {
		return fmt.Errorf("%s", err)
	}
	return nil
}

// unmarshalDuration reads a Go duration string ("15m", "1h") for the two
// duration settings. A JSON number is refused: the unit would be a guess.
func unmarshalDuration(value json.RawMessage, target *time.Duration) error {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return fmt.Errorf("duration must be a string like \"15m\": %w", err)
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("%q is not a duration like \"15m\"", text)
	}
	*target = d
	return nil
}

// DeriveSQLiteRoot fills in the SQLite data root from the PostgreSQL data
// directory when "sqlite_data" was not set. Both dialects' files then live
// under one .data root and move together.
func (s Settings) DeriveSQLiteRoot() string {
	if s.SQLiteDataDir != "" {
		return s.SQLiteDataDir
	}
	return filepath.Join(filepath.Dir(filepath.Dir(s.PGDataDir)), "sqlite")
}

// DeriveMetadataPath fills in the SQLite metadata file location from the
// PostgreSQL data directory when "metadata_path" was not set.
func (s Settings) DeriveMetadataPath() string {
	if s.MetadataPath != "" {
		return s.MetadataPath
	}
	return filepath.Join(filepath.Dir(s.PGDataDir), "metadata.sqlite")
}
