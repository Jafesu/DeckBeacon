// Command deckbeacon is the DeckBeacon SteamOS agent.
//
// Milestone 1 skeleton: only `version` is implemented. Collectors, pairing and the
// device channel arrive in Milestone 2.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		if len(args) > 1 && args[1] == "--json" {
			enc := json.NewEncoder(stdout)
			if err := enc.Encode(buildinfo.Get()); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(stdout, buildinfo.Get())
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: deckbeacon <command>\n\ncommands:\n  version [--json]   print build information")
}
