package dev

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrapi/cli/internal/netproxy"
)

// Without prompts, --set answers, the other questions take their default,
// and a skipped file is copied as it is.
func TestInit_noPrompt(t *testing.T) {
	out := t.TempDir()
	var msg bytes.Buffer
	dir, err := Init(InitOptions{
		Template: "testdata/template", Name: "mystack", Dir: out, NoPrompt: true,
		Vars: map[string]any{"domain": "example.org"}, Cache: t.TempDir(),
	}, &msg)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(out, "mystack") {
		t.Errorf("dir = %s", dir)
	}
	dev, err := os.ReadFile(filepath.Join(dir, "dev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(dev) != "domain: example.org\nplatform: sandbox\n" {
		t.Errorf("dev.yaml:\n%s", dev)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "kayra", "pipeline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "{{ .Subscription.Api }}") {
		t.Errorf("a skipped file was rendered: %s", raw)
	}
	if strings.TrimSpace(msg.String()) != "mystack on example.org" {
		t.Errorf("post message: %q", msg.String())
	}

	// again, without --force: the files it has are left as they are
	if err := os.WriteFile(filepath.Join(dir, "dev.yaml"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(InitOptions{Template: "testdata/template", Name: "mystack", Dir: out, NoPrompt: true, Cache: t.TempDir()}, &msg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "dev.yaml")); string(b) != "edited\n" {
		t.Errorf("dev.yaml overwritten without --force: %s", b)
	}
}

func TestInit_refuses(t *testing.T) {
	for name, o := range map[string]InitOptions{
		"no name":          {Template: "testdata/template", NoPrompt: true},
		"name with a path": {Template: "testdata/template", Name: "a/b", NoPrompt: true},
		"no template":      {Template: "testdata/none", Name: "x", NoPrompt: true},
	} {
		o.Dir, o.Cache = t.TempDir(), t.TempDir()
		if _, err := Init(o, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// dev init clones a remote template through http_proxy, https_proxy.
func TestInit_throughProxy(t *testing.T) {
	for _, v := range netproxy.Vars {
		t.Setenv(v, "")
	}
	netproxy.Setup()
	var got []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Host+r.URL.Path)
		http.NotFound(w, r)
	}))
	defer proxy.Close()
	t.Setenv("http_proxy", proxy.URL)

	_, err := Init(InitOptions{
		Template: "http://git.example.com/infrapi/dev", Name: "x", Dir: t.TempDir(), NoPrompt: true, Cache: t.TempDir(),
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("cloned from a proxy answering 404")
	}
	if len(got) == 0 || got[0] != "git.example.com/infrapi/dev/info/refs" {
		t.Errorf("the proxy got %v", got)
	}
}
