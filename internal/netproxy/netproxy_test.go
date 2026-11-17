package netproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorder is a proxy that records the requests it gets, and answers them.
func recorder(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.String())
		mu.Unlock()
		_, _ = io.WriteString(w, "via proxy")
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, got...) }
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, v := range Vars {
		t.Setenv(v, "")
	}
}

// http_proxy, read when the request is made, routes http:// URLs; a host in
// no_proxy and localhost are reached directly.
func TestSetup(t *testing.T) {
	clearEnv(t)
	Setup()
	proxy, got := recorder(t)

	t.Setenv("http_proxy", proxy.URL)
	resp, err := http.Get("http://kayra.example.com/apis")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "via proxy" || strings.Join(got(), ",") != "GET http://kayra.example.com/apis" {
		t.Fatalf("through the proxy: %q, %v", body, got())
	}

	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "direct") }))
	defer direct.Close()
	resp, err = http.Get(direct.URL) // 127.0.0.1: never through a proxy
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	t.Setenv("NO_PROXY", ".example.com")
	if _, err := http.Get("http://kayra.example.com/apis"); err == nil {
		t.Error("kayra.example.com, in NO_PROXY, answered: through the proxy")
	}
	if len(got()) != 1 {
		t.Errorf("the proxy got %v", got())
	}
}

// Set hides a proxy's password.
func TestSet(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTPS_PROXY", "http://jdoe:secret@proxy.example.com:3128")
	t.Setenv("no_proxy", "localhost,.example.com")
	got := Set()
	if len(got) != 2 || got[0][1] != "http://jdoe:xxxxx@proxy.example.com:3128" || got[1] != [2]string{"no_proxy", "localhost,.example.com"} {
		t.Errorf("Set = %v", got)
	}
}
