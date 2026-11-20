package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReadDotenv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.env")
	data := "# a comment\nA=1\n  B = \"two words\"\nC='x'\n\nnot a line\nD=\n"
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := readDotenv(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A=1", "B=two words", "C=x", "D="}
	if !slices.Equal(env, want) {
		t.Fatalf("readDotenv: %q, want %q", env, want)
	}
}
