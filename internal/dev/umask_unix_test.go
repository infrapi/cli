//go:build unix

package dev

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// With a umask of 077, the files the containers mount are still readable by
// them, and the keys still the owner's only.
func TestRender_umask077(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	s, _ := testStack(t, "", StackOptions{})
	for _, f := range mounted {
		p := filepath.Join(s.Dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Render(false); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(s.Out, "kayra.yaml"):              0o644,
		filepath.Join(s.Out, "users.yaml"):              0o644,
		filepath.Join(s.Out, "routes"):                  0o755,
		filepath.Join(s.Out, "routes", "00-kayra.json"): 0o644,
		filepath.Join(s.Dir, "apisix", "config.yaml"):   0o644,
		filepath.Join(s.Dir, "apisix", "routes.sh"):     0o644,
		filepath.Join(s.Out, "keys", "self.pem"):        0o600,
		s.Out:                                           0o700,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s: %v, want %v", path, st.Mode().Perm(), want)
		}
	}

	// a file left unreadable since is put back on the next render, unchanged
	kayra := filepath.Join(s.Out, "kayra.yaml")
	if err := os.Chmod(kayra, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Render(false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(kayra); st.Mode().Perm() != 0o644 {
		t.Errorf("kayra.yaml after a render: %v", st.Mode().Perm())
	}
}
