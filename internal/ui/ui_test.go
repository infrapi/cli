package ui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

var escape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// forced makes a bytes.Buffer take colors, as a terminal would.
func forced(t *testing.T) {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	SetNoColor(false)
	t.Cleanup(func() { SetNoColor(false) })
}

// Not a terminal: no escape sequence at all, what a script reads.
func TestNoTerminalIsPlain(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "")
	var b bytes.Buffer
	if p := Profile(&b); p != colorprofile.NoTTY {
		t.Fatalf("profile of a buffer = %v, want NoTTY", p)
	}
	_, _ = Writer(&b).Write([]byte(OK("ok") + " " + Fail("no") + "\n"))
	if err := Highlight(&b, "json", `{"a": 1}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "ok no\n{\"a\": 1}\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// NO_COLOR, any value but empty, and --no-color turn colors off, even
// where they would be on.
func TestNoColor(t *testing.T) {
	forced(t)
	var b bytes.Buffer
	if !Color(&b) {
		t.Fatal("forced colors are off")
	}
	t.Setenv("NO_COLOR", "yes")
	if Color(&b) {
		t.Error("NO_COLOR=yes left colors on")
	}
	t.Setenv("NO_COLOR", "")
	SetNoColor(true)
	if Color(&b) {
		t.Error("--no-color left colors on")
	}
	_, _ = Writer(&b).Write([]byte(Fail("no")))
	if escape.MatchString(b.String()) {
		t.Errorf("--no-color wrote %q", b.String())
	}
}

// With colors, JSON, YAML and diffs are highlighted, their text unchanged.
func TestHighlight(t *testing.T) {
	forced(t)
	for lang, src := range map[string]string{
		"json": "{\n  \"name\": \"compute\"\n}\n",
		"yaml": "name: compute\nport: 8080\n",
		"diff": "--- a/env\n+++ b/env\n-PORT=1\n+PORT=2\n",
	} {
		var b bytes.Buffer
		if err := Highlight(&b, lang, src); err != nil {
			t.Fatal(err)
		}
		if !escape.MatchString(b.String()) {
			t.Errorf("%s: no color in %q", lang, b.String())
		}
		if got := escape.ReplaceAllString(b.String(), ""); got != src {
			t.Errorf("%s: text changed: %q, want %q", lang, got, src)
		}
	}
}

// A colored table stays aligned: the styles come after the alignment.
func TestTableAligned(t *testing.T) {
	forced(t)
	var b bytes.Buffer
	err := Table(&b, []string{"CURRENT", "NAME"}, [][]string{{"", "dev-admin"}, {"*", "dev-jdoe"}}, func(i int) bool { return i == 1 })
	if err != nil {
		t.Fatal(err)
	}
	if !escape.MatchString(b.String()) {
		t.Fatalf("no color in %q", b.String())
	}
	got := escape.ReplaceAllString(b.String(), "")
	want := "CURRENT  NAME\n         dev-admin\n*        dev-jdoe\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(b.String(), OK("*        dev-jdoe")) {
		t.Errorf("the current row is not highlighted: %q", b.String())
	}
}
