package useragent

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Transport sets the User-Agent of a request without one, keeps one set,
// and leaves the caller's request as it was.
func TestTransport(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.UserAgent())
	}))
	defer srv.Close()
	c := &http.Client{Transport: Transport("infrapi-cli/1.2.3", http.DefaultTransport)}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if ua := req.Header.Get("User-Agent"); ua != "" {
		t.Errorf("the caller's request changed: User-Agent %q", ua)
	}

	own, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	own.Header.Set("User-Agent", "mine/1")
	resp, err = c.Do(own)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(got) != 2 || got[0] != "infrapi-cli/1.2.3" || got[1] != "mine/1" {
		t.Errorf("User-Agents %q, want infrapi-cli/1.2.3 then mine/1", got)
	}
}
