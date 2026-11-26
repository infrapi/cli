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
		got = append(got, r.Method+" "+r.URL.String()+" "+r.UserAgent())
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
	Setup("infrapi-cli/v1.2.3")
	Setup("infrapi-cli/again") // once only
	proxy, got := recorder(t)

	t.Setenv("http_proxy", proxy.URL)
	resp, err := http.Get("http://kayra.example.com/apis")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "via proxy" || strings.Join(got(), ",") != "GET http://kayra.example.com/apis infrapi-cli/v1.2.3" {
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

// A User-Agent the caller set is kept, and the caller's request untouched.
func TestSetupKeepsUserAgent(t *testing.T) {
	clearEnv(t)
	Setup("infrapi-cli/v1.2.3")
	proxy, got := recorder(t)
	t.Setenv("http_proxy", proxy.URL)
	req, err := http.NewRequest(http.MethodGet, "http://kayra.example.com/apis", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "custom/1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got(), ",") != "GET http://kayra.example.com/apis custom/1" {
		t.Fatalf("the proxy got %v", got())
	}

	req, err = http.NewRequest(http.MethodGet, "http://kayra.example.com/apis", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("User-Agent") != "" {
		t.Errorf("the caller's request got User-Agent %q", req.Header.Get("User-Agent"))
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
