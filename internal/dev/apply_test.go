package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeKayra answers like Kayra: its discovery document, upserts, and
// subscriptions it remembers.
type fakeKayra struct {
	domain string
	mu     sync.Mutex
	calls  []string
	subs   map[string]bool
}

func (f *fakeKayra) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/.well-known/kayra.json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"domain": f.domain, "apis": map[string]string{
			"system": "/apis/system." + f.domain + "/v1", "catalog": "/apis/catalog." + f.domain + "/v1",
			"authorization": "/apis/authorization." + f.domain + "/v1",
		}})
		return
	}
	if r.Header.Get("Authorization") != "Bearer admin" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if strings.Contains(r.URL.Path, "/subscriptions") {
		if r.Method == http.MethodGet {
			if !f.subs[r.URL.Path] {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		var body struct {
			API string `json:"api"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.subs[r.URL.Path+"/"+body.API] = true
	}
	w.WriteHeader(http.StatusCreated)
}

func count(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestApply(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, strings.Join([]string{
		"domain: example.org",
		"apis: [{name: compute}, {name: dev}]",
		"tenants:",
		"  - {name: core, admins: [sandbox-core-sysadmin], subscriptions: all}",
		"  - {name: sre, admins: [sandbox-core-sysadmin], subscriptions: [dev]}",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeKayra{domain: "example.org", subs: map[string]bool{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	k := &Kayra{Server: srv.URL, Token: "admin"}

	var log bytes.Buffer
	if err := Apply(context.Background(), c, k, &log); err != nil {
		t.Fatal(err)
	}
	catalog := "/apis/catalog.example.org/v1"
	if n := count(f.calls, "POST "+catalog+"/apiregistrations"); n != 2 {
		t.Errorf("%d registrations, want 2: %v", n, f.calls)
	}
	if n := count(f.calls, "POST /apis/system.example.org/v1/namespaces"); n != 2 {
		t.Errorf("%d namespaces, want 2", n)
	}
	if n := count(f.calls, "POST /apis/authorization.example.org/v1/namespaces/core/role"); n != 2 {
		t.Errorf("core: %d role and rolebinding, want 2", n)
	}
	if n := count(f.calls, "POST "+catalog+"/namespaces/"); n != 3 {
		t.Errorf("%d subscriptions, want 3 (2 core, 1 sre)", n)
	}
	if n := count(f.calls, "PUT "+catalog+"/apiregistrations/"); n != 2 {
		t.Errorf("%d syncs, want 2", n)
	}

	// again: the subscriptions are there, none is created
	f.calls = nil
	if err := Apply(context.Background(), c, k, &log); err != nil {
		t.Fatal(err)
	}
	if n := count(f.calls, "POST "+catalog+"/namespaces/"); n != 0 {
		t.Errorf("second run created %d subscriptions, want 0", n)
	}
}

// A Kayra on another domain than dev.yaml's: refused before any write.
func TestApply_otherDomain(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, "domain: example.org\napis: [{name: compute}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeKayra{domain: "infrapi.io", subs: map[string]bool{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	err = Apply(context.Background(), c, &Kayra{Server: srv.URL, Token: "admin"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `domain "infrapi.io"`) || len(f.calls) != 0 {
		t.Fatalf("err %v, calls %v", err, f.calls)
	}
}

// A refused write stops it, with Kayra's answer.
func TestApply_refused(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, "apis: [{name: compute}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeKayra{domain: "infrapi.io", subs: map[string]bool{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	err = Apply(context.Background(), c, &Kayra{Server: srv.URL, Token: "nope"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
}
