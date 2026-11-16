package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGateway(t *testing.T) {
	var method, path, key string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, key, body = r.Method, r.URL.Path, r.Header.Get("X-API-KEY"), nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	g := &Gateway{URL: srv.URL, Key: "k"}

	if err := g.AddAPI(context.Background(), "compute.example.org", "host.containers.internal:8080"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/apisix/admin/routes/compute.example.org" || key != "k" ||
		body["uri"] != "/apis/compute.example.org/*" {
		t.Fatalf("add: %s %s %s %v", method, path, key, body)
	}
	if err := g.RemoveAPI(context.Background(), "compute.example.org"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete || path != "/apisix/admin/routes/compute.example.org" {
		t.Fatalf("rm: %s %s", method, path)
	}
}
