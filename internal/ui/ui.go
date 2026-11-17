// Package ui is infrapi-cli's terminal output: its styles (lipgloss), its
// syntax highlighting (chroma), and whether a writer gets colors at all.
//
// A writer gets colors when it is a terminal, NO_COLOR is unset or empty
// (https://no-color.org) and --no-color was not given. Otherwise, a pipe or
// a file say, it gets plain text, with no escape sequence: what scripts read
// stays the same.
//
// The styles use the terminal's own 16 colors, so they follow its theme,
// light or dark.
package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"text/tabwriter"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/colorprofile"
)

var noColor atomic.Bool

// SetNoColor turns colors off, or back on where a writer takes them: the
// --no-color flag.
func SetNoColor(v bool) { noColor.Store(v) }

// Profile is the colors w takes: NoTTY, no escape sequence at all, unless
// it is a terminal and neither NO_COLOR nor --no-color turned them off.
func Profile(w io.Writer) colorprofile.Profile {
	if cw, ok := w.(*colorprofile.Writer); ok {
		return cw.Profile
	}
	if noColor.Load() || os.Getenv("NO_COLOR") != "" {
		return colorprofile.NoTTY
	}
	return colorprofile.Detect(w, os.Environ())
}

// Color says whether w takes colors.
func Color(w io.Writer) bool { return Profile(w) > colorprofile.ASCII }

// Writer wraps w: the styled text written to it is downsampled to the
// colors w takes, its escape sequences stripped when it takes none.
func Writer(w io.Writer) io.Writer {
	if cw, ok := w.(*colorprofile.Writer); ok {
		return cw
	}
	return &colorprofile.Writer{Forward: w, Profile: Profile(w)}
}

// Stdout and Stderr are os.Stdout and os.Stderr, wrapped by Writer.
func Stdout() io.Writer { return Writer(os.Stdout) }

// Stderr is os.Stderr, wrapped by Writer.
func Stderr() io.Writer { return Writer(os.Stderr) }

// The styles, in the terminal's own colors. Render them on one line only:
// lipgloss pads a multi-line string's lines to the same width.
var (
	bold    = lipgloss.NewStyle().Bold(true)
	faint   = lipgloss.NewStyle().Faint(true)
	green   = lipgloss.NewStyle().Foreground(lipgloss.Green)
	red     = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	yellow  = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	cyan    = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	magenta = lipgloss.NewStyle().Foreground(lipgloss.Magenta)
	blue    = lipgloss.NewStyle().Foreground(lipgloss.Blue).Underline(true)
)

// Bold is a header, a label.
func Bold(s string) string { return bold.Render(s) }

// Faint is what matters less: a command run, a hint.
func Faint(s string) string { return faint.Render(s) }

// OK is a success.
func OK(s string) string { return green.Render(s) }

// Fail is an error, a failed check.
func Fail(s string) string { return red.Render(s) }

// Warn is a warning.
func Warn(s string) string { return yellow.Render(s) }

// Name is a name: a context, an API, a file.
func Name(s string) string { return cyan.Render(s) }

// Cmd is a command to run.
func Cmd(s string) string { return magenta.Render(s) }

// URL is a link.
func URL(s string) string { return blue.Render(s) }

// Table writes header and rows aligned in columns, kubectl-style: the
// header in bold, a row for which highlight is true in green (the current
// context, say). highlight may be nil.
func Table(w io.Writer, header []string, rows [][]string, highlight func(row int) bool) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	out := Writer(w)
	for i, line := range lines {
		// styled after alignment: escape sequences would count as width
		line = strings.TrimRight(line, " ")
		switch {
		case i == 0:
			line = Bold(line)
		case highlight != nil && highlight(i-1):
			line = OK(line)
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// Highlight writes src to w, syntax-highlighted as lang (json, yaml, diff,
// bash...) in the terminal's 16 colors when w takes colors, as it is
// otherwise.
func Highlight(w io.Writer, lang, src string) error {
	p := Profile(w)
	if p <= colorprofile.ASCII {
		_, err := io.WriteString(w, src)
		return err
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		_, err := io.WriteString(w, src)
		return err
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		_, err := io.WriteString(w, src)
		return err
	}
	return formatters.TTY16.Format(w, codeStyle, it)
}

// codeStyle colors code in the terminal's own 16 colors, so it follows its
// theme, light or dark: each colour below is the one chroma's 16-color
// table maps to that ANSI color. Plain text keeps the terminal's color.
var codeStyle = chroma.MustNewStyle("infrapi", chroma.StyleEntries{
	chroma.NameTag:           "#007f7f", // cyan: a JSON or YAML key
	chroma.NameAttribute:     "#007f7f",
	chroma.LiteralString:     "#007f00", // green
	chroma.LiteralNumber:     "#7f007f", // magenta
	chroma.KeywordConstant:   "#7f7fe0", // yellow: true, false, null
	chroma.Keyword:           "#7f007f",
	chroma.Comment:           "#555555", // bright black
	chroma.GenericDeleted:    "#7f0000", // red
	chroma.GenericInserted:   "#007f00", // green
	chroma.GenericHeading:    "bold",
	chroma.GenericSubheading: "#007f7f", // cyan: @@ ... @@
	chroma.NameBuiltin:       "#00007f", // blue: a command
})

// KeyValues writes pairs as aligned "key: value" lines, the keys in bold.
func KeyValues(w io.Writer, pairs [][2]string) error {
	width := 0
	for _, kv := range pairs {
		width = max(width, len(kv[0])+1)
	}
	out := Writer(w)
	for _, kv := range pairs {
		pad := strings.Repeat(" ", width-len(kv[0]))
		if _, err := fmt.Fprintf(out, "%s%s %s\n", Bold(kv[0]+":"), pad, kv[1]); err != nil {
			return err
		}
	}
	return nil
}
