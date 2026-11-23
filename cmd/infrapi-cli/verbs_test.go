package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// request is one request the test server got.
type request struct {
	method, uri, contentType, body string
	length                         int64
}

// verbServer is an API with an action, vms/reboot, behind discovery.
func verbServer(t *testing.T) (*[]request, func(args ...string) error) {
	t.Helper()
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/apis" {
			_, _ = w.Write([]byte(`[{"api":"compute.infrapi.io","version":"v1","resources":[
				{"name":"vms","namespaced":true},{"name":"vms/reboot","namespaced":true},{"name":"flavors"}]}]`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		got = append(got, request{r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), string(b), r.ContentLength})
		_, _ = w.Write([]byte(`{"name":"web"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	run := func(args ...string) error {
		return newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...))
	}
	if err := run("auth", "ctx", "set", "dev", "--server", srv.URL, "--token", "t"); err != nil {
		t.Fatal(err)
	}
	if err := run("auth", "ctx", "ns", "acme"); err != nil {
		t.Fatal(err)
	}
	return &got, run
}

// An action is <resource>/<action> <name>: PUT .../vms/web/reboot, a body
// only when one is given.
func TestVerbsAction(t *testing.T) {
	got, run := verbServer(t)
	stdout := os.Stdout
	os.Stdout, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer func() { os.Stdout = stdout }()
	for _, args := range [][]string{
		{"update", "vms/reboot", "web"},
		{"update", "vms/reboot", "web", "-d", `{"force":true}`},
		{"get", "vms/reboot", "web"},
	} {
		if err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []request{
		{method: "PUT", uri: "/apis/compute.infrapi.io/v1/namespaces/acme/vms/web/reboot"},
		{method: "PUT", uri: "/apis/compute.infrapi.io/v1/namespaces/acme/vms/web/reboot", contentType: "application/json", body: `{"force":true}`, length: 14},
		{method: "GET", uri: "/apis/compute.infrapi.io/v1/namespaces/acme/vms/web/reboot"},
	}
	for i, w := range want {
		if (*got)[i] != w {
			t.Errorf("request %d: %+v, want %+v", i, (*got)[i], w)
		}
	}
	for args, msg := range map[string][]string{
		"an action on an item":       {"update", "vms/reboot"},
		"update vms/reboot web":      {"update", "vms", "web/reboot"},
		`has no action "stop"`:       {"update", "vms/stop", "web"},
		"a request body is required": {"update", "vms", "web"},
	} {
		if err := run(msg...); err == nil || !strings.Contains(err.Error(), args) {
			t.Errorf("%v: %v, want %q", msg, err, args)
		}
	}
}

// --field-selector is the API's query parameters; --raw any path.
func TestVerbsFieldSelectorAndRaw(t *testing.T) {
	got, run := verbServer(t)
	stdout := os.Stdout
	os.Stdout, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer func() { os.Stdout = stdout }()
	body := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(body, []byte(`{"name":"m1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"get", "vms", "--field-selector", "environment=acme_sandbox,state=running"},
		{"get", "--raw", "/apis/compute.infrapi.io/v1/flavors?size=big"},
		{"create", "--raw", "/apis/compute.infrapi.io/v1/flavors", "-f", body},
		{"delete", "--raw", "/apis/compute.infrapi.io/v1/flavors/m1"},
	} {
		if err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []string{
		"GET /apis/compute.infrapi.io/v1/namespaces/acme/vms?environment=acme_sandbox&state=running",
		"GET /apis/compute.infrapi.io/v1/flavors?size=big",
		`POST /apis/compute.infrapi.io/v1/flavors {"name":"m1"}`,
		"DELETE /apis/compute.infrapi.io/v1/flavors/m1",
	}
	for i, w := range want {
		r := (*got)[i]
		if line := strings.TrimSpace(r.method + " " + r.uri + " " + r.body); line != w {
			t.Errorf("request %d: %s, want %s", i, line, w)
		}
	}
	for msg, args := range map[string][]string{
		"only key=value":       {"get", "vms", "--field-selector", "state!=done"},
		"key=value[,key=value": {"get", "vms", "--field-selector", "state"},
		"a path of the server": {"get", "--raw", "https://evil.example.com/apis"},
		"takes no <resource>":  {"get", "--raw", "/apis", "vms"},
		"no -n":                {"get", "--raw", "/apis", "-n", "acme"},
	} {
		if err := run(args...); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%v: %v, want %q", args, err, msg)
		}
	}
}
