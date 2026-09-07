package main

import (
	"fmt"
	"os"
	"strconv"
)

// defaultWidth is the line width used when the terminal does not say how wide
// it is.
const defaultWidth = 120

// palette holds the ANSI escapes the output uses. Its zero value paints
// nothing, which is what a pipe or a redirect gets.
type palette struct {
	dim    string
	green  string
	yellow string
	cyan   string
	red    string
	reset  string
}

// newPalette returns a painting palette, or a blank one when colour is off.
func newPalette(on bool) palette {
	if !on {
		return palette{}
	}

	return palette{
		dim:    "\x1b[2m",
		green:  "\x1b[32m",
		yellow: "\x1b[33m",
		cyan:   "\x1b[36m",
		red:    "\x1b[31m",
		reset:  "\x1b[0m",
	}
}

// pad renders text in a fixed-width column, colouring it after padding so the
// escape codes never count toward the column width.
func (p palette) pad(colour, text string, width int) string {
	padded := fmt.Sprintf("%-*s", width, text)

	return p.paint(colour, padded)
}

// paint wraps text in a colour.
func (p palette) paint(colour, text string) string {
	if colour == "" {
		return text
	}

	return colour + text + p.reset
}

// colorEnabled reports whether to emit ANSI colour.
//
// Colour goes to a terminal and nowhere else, so redirecting the output to a
// file or piping it into grep gives plain text. NO_COLOR is honoured because
// it is the convention every other tool follows.
func colorEnabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	if term := os.Getenv("TERM"); term == "" || term == "dumb" {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// terminalWidth returns the width to wrap output at.
//
// COLUMNS is read rather than the terminal queried, which keeps this to the
// standard library. An unset or nonsense value falls back to a sane default.
func terminalWidth() int {
	width, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || width < 40 {
		return defaultWidth
	}

	return width
}
