package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Gateway is APISIX's Admin API, at URL with Key.
type Gateway struct {
	URL  string
	Key  string
	HTTP *http.Client
}

// AddAPI routes /apis/<api>/* to upstream, unchanged: the API, and its own
// endpoints under /apis/<api>/<version>/-/, the spec Kayra reads included.
// The route's id is the API's name, so adding it again replaces it.
func (g *Gateway) AddAPI(ctx context.Context, api, upstream string) error {
	return g.PutRoute(ctx, api, map[string]any{
		"uri":      "/apis/" + api + "/*",
		"upstream": map[string]any{"type": "roundrobin", "nodes": map[string]int{upstream: 1}},
	})
}

// PutRoute puts the route id, body as the Admin API takes it: created, or
// replaced as it is.
func (g *Gateway) PutRoute(ctx context.Context, id string, body map[string]any) error {
	return g.call(ctx, http.MethodPut, id, body)
}

// DeleteRoute deletes the route id; one already gone is no error.
func (g *Gateway) DeleteRoute(ctx context.Context, id string) error {
	err := g.call(ctx, http.MethodDelete, id, nil)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusNotFound {
		return nil
	}
	return err
}

// RemoveAPI removes the route AddAPI added.
func (g *Gateway) RemoveAPI(ctx context.Context, api string) error {
	return g.call(ctx, http.MethodDelete, api, nil)
}

func (g *Gateway) call(ctx context.Context, method, id string, body any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.URL, "/")+"/apisix/admin/routes/"+id, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", g.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("APISIX Admin API at %s: %w", g.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{code: resp.StatusCode, msg: fmt.Sprintf("APISIX %s route %s: %d %s", method, id, resp.StatusCode, strings.TrimSpace(string(data)))}
	}
	return nil
}

// statusError is an Admin API answer other than 2xx.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }
