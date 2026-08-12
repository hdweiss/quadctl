package quadlet

import (
	"fmt"
	"os"

	"github.com/fkmiec/quadctl/internal/config"
	"github.com/fkmiec/quadctl/internal/runner"
	"github.com/fkmiec/quadctl/internal/schema"
)

// State is one run of one subcommand: the flags it was given, the directory it is working
// on, and what has been discovered so far. It is threaded through everything below main and
// is expected to change as the run proceeds - which is exactly why the user's configuration
// is not part of it but sits behind Config, read once and left alone (PLAN.md 3.2).
type State struct {
	// Config is the loaded quadctl.ini. Treat it as read-only: it describes the machine, not
	// this invocation, and handlers that write to it would change the meaning of the next
	// directory in the same run.
	Config *config.Config

	QuadletSchemas map[string]map[string]schema.SchemaOption
	Runner         runner.Runner // Executes every external command; swapped for a fake in tests

	IsRootful bool // Derived from the effective uid, not a flag
	IsSystemd bool
	// IsNoSystemd is --no-systemd: the only way to get back to podman-direct mode on a host
	// whose quadctl.ini sets systemd.enabled=true, since the config is read after the flags.
	IsNoSystemd bool

	IsPrintOnly bool
	IsVerbose   bool
	// IsNoColor is --no-color. NO_COLOR in the environment and a non-terminal stdout have
	// the same effect; see core.UseColor.
	IsNoColor bool

	IsFile       bool
	ListDepth    int
	IsListAll    bool
	IsShowAll    bool
	IsLongStatus bool
	Subcommand   string
	SearchDir    string
	PathArg      string // Positional path argument given to the subcommand, if any
	PodmanArgs   string
	RunCmd       string

	// DotQuadletsPath is the scratch directory the current search directory's .quadlets
	// bundle was extracted into, or "" when that directory had none. It is recomputed for
	// every directory scanned: leaving one directory's value in place is what made
	// InitAllQuadlets install the previous directory's extracted files.
	DotQuadletsPath string

	// scratchDirs are every scratch directory created during this run, removed by Cleanup.
	// They outlive parsing because the systemd install commands copy out of them, so they
	// can only be dropped once the run is over.
	scratchDirs []string
}

// newScratchDir makes a private temporary directory for this run and records it so Cleanup
// can remove it. The old code derived the path from the source directory's name, so two runs
// - or two users - collided on it, and it began by RemoveAll'ing whatever was already there.
func (s *State) newScratchDir(purpose string) (string, error) {
	dir, err := os.MkdirTemp("", "quadctl-"+purpose+"-")
	if err != nil {
		return "", fmt.Errorf("creating scratch directory: %w", err)
	}
	s.scratchDirs = append(s.scratchDirs, dir)
	return dir, nil
}

// Cleanup removes the scratch directories this run created. Safe to call more than once, and
// on a State that never made any. main defers it; nothing else should call it, since the
// systemd install commands read from these directories when they run.
func (s *State) Cleanup() {
	for _, dir := range s.scratchDirs {
		_ = os.RemoveAll(dir)
	}
	s.scratchDirs = nil
	s.DotQuadletsPath = ""
}
