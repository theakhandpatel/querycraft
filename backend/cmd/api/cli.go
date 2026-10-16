package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/querycraft/sqlquery/backend/internal/config"
)

// gatewayVersion is the gateway's own version, reported by
// `querycraft version`. It changes when the release changes.
const gatewayVersion = "0.8.0"

// exitError carries a process exit code through cobra's RunE error channel.
// The refusal paths in run already logged why; this only carries the code.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit code %d", e.code) }

// Execute builds the command tree and runs it. Serving is the root's own
// behavior, so a bare `querycraft` (what `make serve` runs) starts the
// gateway, and the same code path answers `querycraft serve`.
func Execute() int {
	root := &cobra.Command{
		Use:   "querycraft",
		Short: "The Querycraft execution gateway.",
		Long: "A JSON HTTP service that runs learner SQL against native PostgreSQL " +
			"and grades it against result contracts. It runs usefully with no database " +
			"configured: SQL execution reports ENVIRONMENT_UNAVAILABLE and progress " +
			"falls back to memory.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve(cmd)
		},
	}
	root.PersistentFlags().String("config", "",
		"config file to read (default: ./querycraft.json)")

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the gateway (the default).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve(cmd)
		},
	}
	serveCmd.Flags().String("addr", "", "listen address; overrides the config file")

	dbCmd := &cobra.Command{
		Use:   "db",
		Short: "Local database maintenance.",
	}
	dbCmd.AddCommand(
		&cobra.Command{
			Use:   "reset",
			Short: "Delete the local PostgreSQL cluster's data directory; the next boot re-applies the commerce dataset.",
			RunE: func(cmd *cobra.Command, args []string) error {
				return dbReset(cmd)
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Report the resolved configuration and what exists on disk.",
			RunE: func(cmd *cobra.Command, args []string) error {
				return dbStatus(cmd)
			},
		},
	)

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the gateway version.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("querycraft %s\n", gatewayVersion)
			return nil
		},
	}, serveCmd, dbCmd)

	if err := root.Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		fmt.Fprintln(os.Stderr, "querycraft:", err)
		return 1
	}
	return 0
}

// serve resolves the settings (defaults, then the config file named by
// --config or ./querycraft.json), applies
// the --addr flag as the last precedence layer, and starts the gateway.
func serve(cmd *cobra.Command) error {
	settings, err := loadSettings(cmd)
	if err != nil {
		return err
	}
	settings = applyAddrFlag(settings, flagValue(cmd, "addr"))
	if code := run(settings); code != 0 {
		return &exitError{code}
	}
	return nil
}

// applyAddrFlag is the command-line layer of the precedence order: a flag that
// was actually passed overrides the environment and the config file. An empty
// flag value means "not passed" and changes nothing.
func applyAddrFlag(settings config.Settings, addr string) config.Settings {
	if addr != "" {
		settings.Addr = addr
	}
	return settings
}

// loadSettings resolves the settings for any subcommand through the shared
// --config flag.
func loadSettings(cmd *cobra.Command) (config.Settings, error) {
	return config.Load(flagValue(cmd, "config"))
}

// flagValue reads a flag the command may not define. The root command serves
// without --addr, so a missing flag is an empty value rather than an error.
func flagValue(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// dbReset deletes the configured local cluster's data directory. It never
// starts a server and never touches an external database: the config decides
// which directory is meant, and a value that does not name a directory is
// refused rather than guessed at.
func dbReset(cmd *cobra.Command) error {
	settings, err := loadSettings(cmd)
	if err != nil {
		return err
	}
	dataDir := filepath.Clean(settings.PGDataDir)
	if dataDir == "" || dataDir == "." || dataDir == string(filepath.Separator) {
		return fmt.Errorf("refusing to delete %q: it does not name a cluster data directory", settings.PGDataDir)
	}
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("removing %s: %w", dataDir, err)
	}
	fmt.Printf("db reset: removed %s; the next boot re-applies the commerce dataset\n", dataDir)
	return nil
}

// dbStatus reports the configuration the gateway would resolve right now and
// what the local paths look like on disk. It starts nothing and writes nothing.
func dbStatus(cmd *cobra.Command) error {
	settings, err := loadSettings(cmd)
	if err != nil {
		return err
	}
	file, err := config.ResolveFile(flagValue(cmd, "config"))
	if err != nil {
		return err
	}
	if file == "" {
		fmt.Println("config file: (none)")
	} else {
		fmt.Printf("config file: %s\n", file)
	}
	fmt.Printf("listen address: %s\n", settings.Addr)

	switch {
	case settings.NoLocalDB:
		fmt.Println("database: local server disabled (no_local_db)")
	case settings.DatabaseURL != "":
		fmt.Println("database: external (database_url)")
	default:
		fmt.Println("database: local server, started at boot")
	}

	fmt.Printf("cluster data directory: %s", filepath.Clean(settings.PGDataDir))
	printExists(settings.PGDataDir)
	fmt.Printf("binary cache: %s", filepath.Clean(settings.PGBinariesDir))
	printExists(settings.PGBinariesDir)

	switch {
	case settings.AdminDatabaseURL != "":
		fmt.Println("metadata: postgresql (admin_database_url)")
	case strings.EqualFold(settings.Metadata, "sqlite"):
		fmt.Printf("metadata: sqlite (%s)\n", settings.DeriveMetadataPath())
	case settings.DatabaseURL != "":
		fmt.Println("metadata: postgresql (the execution DSN)")
	default:
		fmt.Println("metadata: memory (no DSN)")
	}

	switch kind := strings.ToLower(strings.TrimSpace(settings.AIProvider)); kind {
	case "", "off", "none", "disabled":
		fmt.Println("ai provider: off")
	case "fake", "deterministic":
		fmt.Println("ai provider: fake")
	default:
		fmt.Printf("ai provider: %s (model %s)\n", settings.AIProvider, settings.AIModel)
	}
	return nil
}

func printExists(path string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Println(" (exists)")
	} else {
		fmt.Println(" (absent)")
	}
}
