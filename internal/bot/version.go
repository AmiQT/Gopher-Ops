package bot

import (
	"fmt"
	"runtime/debug"
)

// Version is populated from the Go build's embedded VCS info. It surfaces in
// /status so operators always know which commit they're talking to.
var Version = func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var commit, when string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if commit == "" {
		return "dev"
	}
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	if modified {
		short += "-dirty"
	}
	if when != "" {
		return fmt.Sprintf("%s (%s)", short, when)
	}
	return short
}()
