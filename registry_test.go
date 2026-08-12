package main

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/fkmiec/quadctl/internal/util"
)

func testRegistry(t *testing.T) (*registry, *util.State) {
	t.Helper()
	quadctl := &util.State{ListDepth: defaultListDepth}
	r := newRegistry(quadctl)
	// Usage goes to stderr; tests that provoke it only care that parsing failed.
	r.global.SetOutput(io.Discard)
	r.global.Usage = func() {}
	for _, c := range r.commands {
		c.flagSet.SetOutput(io.Discard)
		c.flagSet.Usage = func() {}
	}
	return r, quadctl
}

// TestRegistryIsWellFormed checks the invariants the dispatch and usage code assume, so a
// half-filled row in the table fails here rather than at runtime.
func TestRegistryIsWellFormed(t *testing.T) {
	r, _ := testRegistry(t)

	seen := map[string]string{}
	for _, c := range r.commands {
		if c.Run == nil {
			t.Errorf("%s: no Run handler", c.Name)
		}
		if c.Summary == "" || c.Synopsis == "" {
			t.Errorf("%s: missing help text", c.Name)
		}
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			if other, dup := seen[name]; dup {
				t.Errorf("%q is claimed by both %s and %s", name, other, c.Name)
			}
			seen[name] = c.Name
		}
		// Every subcommand accepts the global flags too - that is the whole point of
		// registering them per flag set.
		for _, f := range globalFlags {
			if c.flagSet.Lookup(f.Name) == nil {
				t.Errorf("%s: does not accept the global --%s", c.Name, f.Name)
			}
			if f.Short != "" && c.flagSet.Lookup(f.Short) == nil {
				t.Errorf("%s: does not accept the global -%s", c.Name, f.Short)
			}
		}
		for _, f := range c.Flags {
			if c.flagSet.Lookup(f.Name) == nil {
				t.Errorf("%s: --%s declared but not registered", c.Name, f.Name)
			}
			if f.Short != "" && c.flagSet.Lookup(f.Short) == nil {
				t.Errorf("%s: -%s declared but not registered", c.Name, f.Short)
			}
		}
	}
}

// TestSystemdFlagAcceptedEitherSide covers TODO.md section 3: 'quadctl start -s' used to die
// with "flag provided but not defined: -s" while start's own help advertised the flag.
func TestSystemdFlagAcceptedEitherSide(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want bool
	}{
		{"before the subcommand", []string{"-s", "start"}, true},
		{"after the subcommand", []string{"start", "-s"}, true},
		{"long form after", []string{"start", "--systemd"}, true},
		{"not given at all", []string{"start"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, quadctl := testRegistry(t)
			if err := r.parseGlobalFlags(tt.argv); err != nil {
				t.Fatalf("parseGlobalFlags(%v): %v", tt.argv, err)
			}
			c := r.byName[r.args[0]]
			if err := c.flagSet.Parse(r.args[1:]); err != nil {
				t.Fatalf("parsing %v: %v", r.args[1:], err)
			}
			if quadctl.IsSystemd != tt.want {
				t.Errorf("IsSystemd = %v, want %v", quadctl.IsSystemd, tt.want)
			}
		})
	}
}

// TestResolveSystemdMode covers TODO.md section 3: systemd.enabled=true in quadctl.ini used
// to be unconditional, so a host configured that way had no way back to podman-direct mode
// and 'quadctl run' was permanently unreachable.
func TestResolveSystemdMode(t *testing.T) {
	tests := []struct {
		name       string
		configured bool // systemd.enabled
		systemd    bool // -s
		noSystemd  bool // --no-systemd
		want       bool
		wantErr    bool
	}{
		{name: "neither", want: false},
		{name: "-s alone", systemd: true, want: true},
		{name: "systemd.enabled alone", configured: true, want: true},
		{name: "--no-systemd overrides the config", configured: true, noSystemd: true, want: false},
		{name: "--no-systemd alone is a no-op", noSystemd: true, want: false},
		{name: "-s with --no-systemd contradicts", systemd: true, noSystemd: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quadctl := &util.State{IsSystemd: tt.systemd, IsNoSystemd: tt.noSystemd}
			cfg := util.DefaultConfig()
			cfg.SystemdEnabled = tt.configured

			err := resolveSystemdMode(quadctl, cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error for contradictory flags")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSystemdMode: %v", err)
			}
			if quadctl.IsSystemd != tt.want {
				t.Errorf("IsSystemd = %v, want %v", quadctl.IsSystemd, tt.want)
			}
		})
	}
}

// TestAliasResolvesToCanonicalName keeps the alias out of everything downstream: core
// compares Subcommand against canonical names only.
func TestAliasResolvesToCanonicalName(t *testing.T) {
	r, _ := testRegistry(t)
	for alias, want := range map[string]string{"rm": "remove", "ls": "list", "remove": "remove"} {
		if got := r.byName[alias]; got == nil || got.Name != want {
			t.Errorf("byName[%q] = %v, want %s", alias, got, want)
		}
	}
}

// TestUnknownFlagIsAUsageError checks that a bad flag comes back as an error rather than
// exiting the process from inside the flag package.
func TestUnknownFlagIsAUsageError(t *testing.T) {
	r, _ := testRegistry(t)
	if err := r.byName["create"].flagSet.Parse([]string{"-nope"}); err == nil {
		t.Fatal("parsing -nope succeeded, want an error")
	}
	if err := usageError(flag.ErrHelp); err != errHelp {
		t.Errorf("usageError(flag.ErrHelp) = %v, want errHelp", err)
	}
}

// TestSubcommandUsageUsesItsOwnFlags covers TODO.md section 3: PrintLogsUsage printed the
// stats flag set, so 'quadctl logs -h' advertised -a and -f, neither of which it accepts.
func TestSubcommandUsageUsesItsOwnFlags(t *testing.T) {
	r, _ := testRegistry(t)
	for _, c := range r.commands {
		var b strings.Builder
		printFlags(&b, append(append([]flagSpec{}, c.Flags...), globalFlags...))
		help := b.String()

		for _, f := range c.Flags {
			if !strings.Contains(help, "--"+f.Name) {
				t.Errorf("%s help omits --%s", c.Name, f.Name)
			}
		}
		// A flag the subcommand doesn't declare must not show up in its help.
		for _, f := range []flagSpec{flagFile, flagPrint, flagVerbose, flagAll, flagDepth, flagLong, flagExec} {
			declared := false
			for _, own := range c.Flags {
				if own.Name == f.Name {
					declared = true
				}
			}
			if !declared && strings.Contains(help, "--"+f.Name+" ") {
				t.Errorf("%s help advertises --%s, which it does not accept", c.Name, f.Name)
			}
		}
		// Short and long form are one entry, not two (TODO.md section 3).
		if n := strings.Count(help, "\n"); n != len(c.Flags)+len(globalFlags) {
			t.Errorf("%s help has %d flag lines, want %d", c.Name, n, len(c.Flags)+len(globalFlags))
		}
	}
}
