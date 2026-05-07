package output

import "os"

// ANSI escape sequences used by the text and query-table renderers.
// They are emptied at init time if the NO_COLOR environment variable is
// set (any value, even empty), per the https://no-color.org informal
// standard. This makes the output safe to pipe into tools that don't
// strip ANSI, and respects the user's preference without a CLI flag.
var (
	ansiBold            = "\033[1m"
	ansiReset           = "\033[0m"
	ansiItalic          = "\033[3m"
	ansiMuted           = "\033[38;5;243m"
	ansiMutedItalic     = "\033[3;38;5;243m"
	ansiMutedBoldItalic = "\033[1;3;38;5;243m"
)

func init() {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		ansiBold = ""
		ansiReset = ""
		ansiItalic = ""
		ansiMuted = ""
		ansiMutedItalic = ""
		ansiMutedBoldItalic = ""
	}
}
