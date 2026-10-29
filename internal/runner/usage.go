package runner

import (
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
)

const (
	runSynopsis     = "baton run [options] -- command [args...]"
	runExample      = "baton run --lane render --mem 4G -- npx remotion render Main out.mp4"
	statusSynopsis  = "baton status [--json | --line] [--watch]"
	historySynopsis = "baton history [--json] [--since 24h] [--lane L]"
	holdSynopsis    = "baton hold --lane L --pid $$"
)

// errReported marks a failure that is already on stderr, such as a flag
// error the flag package printed together with the usage text.
var errReported = errors.New("already reported")

// setUsage replaces the flag package's "Usage of x:" text with baton's
// usage line, each option spelled --name, and an optional example:
//
//	usage: baton run [options] -- command [args...]
//
//	  --lane string  lane name or auto (default "auto")
//
//	example:
//	  baton run --lane render --mem 4G -- npx remotion render Main out.mp4
func setUsage(flags *flag.FlagSet, synopsis, example string) {
	flags.Usage = func() {
		out := flags.Output()
		fmt.Fprintf(out, "usage: %s\n\n", synopsis)
		table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		flags.VisitAll(func(option *flag.Flag) {
			kind, text := flag.UnquoteUsage(option)
			name := "--" + option.Name
			if kind != "" {
				name += " " + kind
			}
			fmt.Fprintf(table, "  %s\t%s%s\n", name, text, defaultText(kind, option.DefValue))
		})
		_ = table.Flush()
		if example != "" {
			fmt.Fprintf(out, "\nexample:\n  %s\n", example)
		}
	}
}

func defaultText(kind, value string) string {
	switch value {
	case "", "false", "0", "0s":
		return ""
	}
	if kind == "string" {
		return fmt.Sprintf(" (default %q)", value)
	}
	return fmt.Sprintf(" (default %s)", value)
}
