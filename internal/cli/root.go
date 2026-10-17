// Package cli wires coachctl's commands. Conventions every command follows:
// concise text by default and --json for scripts; errors on stderr that say
// how to fix the call; exit codes 0 ok, 1 failure, 2 usage, 3 upstream API;
// no interactive prompts; --dry-run on every external write.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/apierr"
	"github.com/OliverKrr/coachctl/internal/config"
	"github.com/OliverKrr/coachctl/internal/store"
	"github.com/spf13/cobra"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitFailure  = 1
	ExitUsage    = 2
	ExitUpstream = 3
)

// App carries what every command needs. Tests build one with buffers.
type App struct {
	Version string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// Now is the clock. COACHCTL_NOW (RFC 3339) pins it, for reproducible
	// output in tests and examples.
	Now func() time.Time

	JSON bool
	cfg  *config.Config
}

// usageError marks a bad invocation found by a command itself.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

// Main runs coachctl and returns the process exit code.
func Main(version string, args []string) int {
	app := &App{Version: version, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Now: time.Now}
	if v := os.Getenv("COACHCTL_NOW"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			_, _ = fmt.Fprintf(app.Stderr, "coachctl: COACHCTL_NOW %q is not RFC 3339\n", v)
			return ExitUsage
		}
		app.Now = func() time.Time { return t }
	}
	return app.Run(args)
}

// Run executes one command line.
func (a *App) Run(args []string) int {
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	_, _ = fmt.Fprintf(a.Stderr, "coachctl: %s\n", err)
	return exitCode(err)
}

func exitCode(err error) int {
	var usage *usageError
	var storeUsage *store.UsageError
	var upstream *apierr.Error
	switch {
	case errors.As(err, &upstream):
		return ExitUpstream
	case errors.As(err, &usage), errors.As(err, &storeUsage), errors.Is(err, store.ErrNotFound):
		return ExitUsage
	case isCobraUsage(err):
		return ExitUsage
	default:
		return ExitFailure
	}
}

// isCobraUsage recognises cobra's own argument and flag errors, which it
// returns as plain errors.
func isCobraUsage(err error) bool {
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "accepts ", "requires at least", "requires exactly", "invalid argument", "flag needs an argument", "required flag"} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "coachctl",
		Short: "Single-user coaching CLI for Claude Code",
		Long: `coachctl keeps a coaching record as plain files in a git workspace and talks
to Intervals.icu, Hevy and Telegram with your own keys.

Start a coaching session with ` + "`coachctl session start`" + `. New here? Run ` + "`coachctl setup`" + `.`,
		Version:       a.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&a.JSON, "json", false, "print JSON instead of text")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return usagef("%s (see `%s --help`)", err, c.CommandPath())
	})
	root.AddCommand(
		a.sessionCmd(),
		a.journalCmd(),
		a.itemCmd(),
		a.metricCmd(),
		a.icuCmd(),
		a.hevyCmd(),
		a.notifyCmd(),
		a.setupCmd(),
		a.initCmd(),
		a.skillCmd(),
		a.doctorCmd(),
		a.importCmd(),
	)
	return root
}

func (a *App) config() (*config.Config, error) {
	if a.cfg != nil {
		return a.cfg, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	a.cfg = cfg
	return cfg, nil
}

func (a *App) location() (*time.Location, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	return cfg.Location()
}

func (a *App) store() (*store.Store, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	home, err := cfg.Home()
	if err != nil {
		return nil, &usageError{err.Error()}
	}
	loc, err := cfg.Location()
	if err != nil {
		return nil, &usageError{err.Error()}
	}
	return store.Open(home, loc, a.Now)
}

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func (a *App) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.Stdout, format, args...)
}

// readStdin reads the whole of stdin, refusing a terminal: coachctl never
// waits for typed input.
func (a *App) readStdin(what string) (string, error) {
	if f, ok := a.Stdin.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", usagef("%s is read from stdin, e.g. `coachctl ... <<'EOF'` with the text, then EOF", what)
		}
	}
	data, err := io.ReadAll(a.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// textArg takes text from positional args when given, else from stdin.
func (a *App) textArg(args []string, what string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	return a.readStdin(what)
}
