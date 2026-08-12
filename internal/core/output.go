package core

import (
	"fmt"
	"os"
	"strings"

	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/jedib0t/go-pretty/v6/table"
)

// InfoPrefix marks a message as commentary on how a command was built - a restart policy
// worth knowing about, a container that will start with its pod - rather than a report that
// something the user wrote could not be used. Commentary is shown only with -v; everything
// else in Command.Warnings is shown at default verbosity, because an option silently dropped
// is the failure mode this project spent Phase 0 chasing.
const InfoPrefix = "[INFO] "

// isTerminal reports whether f is attached to a terminal rather than a pipe or a file.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// UseColor decides whether to emit ANSI escapes. Colour used to go out unconditionally, so
// `quadctl ps > out.txt` wrote escape codes into the file. Honours --no-color, the NO_COLOR
// convention (https://no-color.org) and TERM=dumb, and otherwise only colours a terminal.
func UseColor(quadctl *quadlet.State) bool {
	if quadctl.IsNoColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(os.Stdout)
}

// tableStyle picks the table rendering to match. The plain style is still a table, just
// without the escape codes that a pipe has no use for.
func tableStyle(quadctl *quadlet.State) table.Style {
	if UseColor(quadctl) {
		return table.StyleColoredYellowWhiteOnBlack
	}
	return table.StyleLight
}

// useSpinner reports whether a command should animate while it runs. A spinner redraws its
// line, which only means anything on a terminal; into a pipe it is noise, and in print mode
// nothing runs at all.
func useSpinner(quadctl *quadlet.State) bool {
	return !quadctl.IsPrintOnly && isTerminal(os.Stdout)
}

// label renders the one-line description shown beside a command. Output used to mix
// "Systemd stopping .container app" with "Starting .container app", printing the raw file
// extension as though it were a word; every command now reads the same way, with the type as
// a noun (TODO.md section 4).
func label(verb, kind, name string) string {
	kind = strings.TrimPrefix(kind, ".")
	if kind == "" || name == "" {
		return strings.TrimSpace(verb + " " + kind + " " + name)
	}
	return fmt.Sprintf("%s %s %s", verb, kind, name)
}

// quadletLabel is label for the podman resource a quadlet describes.
func quadletLabel(verb string, q *quadlet.Quadlet) string {
	return label(verb, q.Type, q.DisplayName())
}

// unitLabel is label for the systemd unit a quadlet generates. It names the unit rather than
// the podman resource, because that is what the systemctl command being run acts on.
func unitLabel(verb string, q *quadlet.Quadlet) string {
	return label(verb, q.Type, q.ServiceName)
}
