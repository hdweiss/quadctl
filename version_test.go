package main

import (
	"strings"
	"testing"
)

// TestVersionFlagStopsBeforeAnySubcommand covers the reason --version is not a global flag:
// it is answered during the global parse, before a subcommand exists to be missing, and it
// reports the same "stop, successfully" sentinel that -h does.
func TestVersionFlagStopsBeforeAnySubcommand(t *testing.T) {
	r, _ := testRegistry(t)
	t.Cleanup(func() { showVersion = false })

	// No subcommand. Without the --version check this is the empty-invocation usage error.
	if err := r.parseGlobalFlags([]string{"--version"}); err != errHelp { //nolint:errorlint // sentinel identity is the point
		t.Fatalf("parseGlobalFlags(--version) = %v, want errHelp", err)
	}
	if code := fail(errHelp); code != 0 {
		t.Errorf("exit code for --version = %d, want 0", code)
	}
}

// TestVersionIsNotASubcommandFlag: --version on a subcommand is a usage error, not a silent
// no-op. The flag catalogue makes it easy to hand a flag to every subcommand by accident.
func TestVersionIsNotASubcommandFlag(t *testing.T) {
	r, _ := testRegistry(t)
	for _, c := range r.commands {
		if c.flagSet.Lookup(flagVersion.Name) != nil {
			t.Errorf("%s: registers --%s, which nothing checks after the global parse",
				c.Name, flagVersion.Name)
		}
	}
}

// TestPrintVersionFallsBackToBuildInfo: an unstamped binary - what `go build` and `go test`
// produce - still has to name a version rather than print a blank line.
func TestPrintVersionFallsBackToBuildInfo(t *testing.T) {
	var out strings.Builder
	printVersion(&out)

	got := out.String()
	first, _, _ := strings.Cut(got, "\n")
	if !strings.HasPrefix(first, toolName+" ") || strings.TrimSpace(first) == toolName {
		t.Errorf("first line %q does not name a version", first)
	}
	// The Go line is unconditional: it is the one fact no build can fail to know.
	if !strings.Contains(got, "go:") {
		t.Errorf("printVersion() = %q, missing the toolchain line", got)
	}
}

func TestShortCommit(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	if got := short(full); got != "0123456789ab" {
		t.Errorf("short(%q) = %q", full, got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short(%q) = %q, want it left alone", "abc", got)
	}
}
