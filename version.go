package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Stamped at link time by GoReleaser through -ldflags -X (PLAN.md 6.4). They are deliberately
// left empty rather than defaulted to "dev": an empty value means "nobody stamped this", which
// is the signal printVersion needs to fall back to the build info the toolchain records.
var (
	version = ""
	commit  = ""
	date    = ""
)

// showVersion is set by --version. It is not part of quadlet.State because it isn't part of
// what a run does - it is answered before a subcommand is ever resolved.
var showVersion bool

// printVersion writes the one-line version banner. A release binary has everything stamped
// into it; a `go build` from a checkout has nothing, and a `go install` of the module has the
// module version and the VCS stamps the toolchain embeds. Fall back through all three so that
// a bug report from any of them names something specific.
func printVersion(w io.Writer) {
	v, c, d := version, commit, date

	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" {
			v = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if c == "" {
					c = setting.Value
				}
			case "vcs.time":
				if d == "" {
					d = setting.Value
				}
			}
		}
	}
	if v == "" {
		v = "unknown"
	}

	fmt.Fprintf(w, "%s %s\n", toolName, v)
	if c != "" {
		// Full hashes are what the stamps carry and what `git show` wants back, but the first
		// twelve are what anyone reads.
		fmt.Fprintf(w, "commit: %s\n", short(c))
	}
	if d != "" {
		fmt.Fprintf(w, "built:  %s\n", d)
	}
	fmt.Fprintf(w, "go:     %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

const shortCommitLen = 12

func short(commit string) string {
	if len(commit) <= shortCommitLen {
		return commit
	}
	return commit[:shortCommitLen]
}
