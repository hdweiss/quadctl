// Package podman is everything quadctl knows about podman itself: how to spell a quadlet
// option as podman arguments, and how to ask podman what currently exists.
//
// OptionArgs is the translation half - one quadlet key/value in, argv out, using the
// templates in internal/schema. ResourceExists and ContainerPS are the query half: read-only
// questions a handler asks while it is still deciding which commands to build.
package podman

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/schema"
)

// templateOption is what a schema option's podman template renders against: the template
// text refers to {{.Key}} and {{.Value}}.
type templateOption struct {
	Key   string
	Value string
}

// valuePlaceholder stands in for an option's value while its podman template is rendered, so
// the rendered fragment can be cut into arguments without the value's own spaces being read
// as separators. Nothing a quadlet file can contain looks like it.
//
// This assumes a template only ever emits the value, never branches on it - true of every
// option in the schema, and worth rechecking if one ever grows an {{if}}.
const valuePlaceholder = "\x00quadctl-value\x00"

// OptionArgs renders one quadlet key/value into the podman arguments it stands
// for. A template is a command-line fragment: it may expand to a flag and its value
// ("{{.Key}} {{.Value}}"), to a single token ("--driver={{.Value}}"), to a flag that embeds
// the value ("--security-opt apparmor={{.Value}}"), or to a flag alone ("--read-only"). So it
// is rendered with the placeholder, cut into arguments, and only then given the real value —
// cutting the rendered text itself is what turned Environment=GREETING="hello world" into two
// arguments and a container that never saw the second word.
func OptionArgs(qType string, options map[string]schema.SchemaOption, k string, v string) ([]string, error) {
	opt, ok := options[k]
	if !ok {
		return nil, fmt.Errorf("Quadlet %s option not defined: %s", qType, k)
	}

	var buf bytes.Buffer
	if err := opt.PodmanTemplateParsed.Execute(&buf, templateOption{Key: opt.PodmanKey, Value: valuePlaceholder}); err != nil {
		return nil, fmt.Errorf("Error formatting %s option %s: %w", qType, k, err)
	}

	args := quadlet.ParseFields(buf.String())
	for i, arg := range args {
		args[i] = strings.ReplaceAll(arg, valuePlaceholder, v)
	}
	return args, nil
}
