// Package netproxy routes infrapi-cli's outgoing HTTP through the proxy the
// environment names, as curl and git do:
//
//	https_proxy, HTTPS_PROXY   for https:// URLs
//	http_proxy, HTTP_PROXY     for http:// URLs
//	no_proxy, NO_PROXY         hosts, domains (.example.com) or CIDRs to reach
//	                           directly; localhost and loopback addresses
//	                           always are
//
// Every client of infrapi-cli goes through http.DefaultTransport: Kayra's
// API, the login and APISIX's Admin API. Setup makes it read the
// environment on each request, where net/http reads it once, and name
// infrapi-cli in the User-Agent, where Go's default, Go-http-client/1.1,
// tells the called side's logs nothing.
package netproxy

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// Vars are the proxy variables, both cases, as the environment may have
// them.
var Vars = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"}

// Setup makes http.DefaultTransport take its proxy from the environment,
// and set userAgent on every request that has no User-Agent of its own.
func Setup(userAgent string) {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return // set up already
	}
	t.Proxy = Proxy
	http.DefaultTransport = agent{next: t, value: userAgent}
}

// agent sets the User-Agent of a request without one, on a clone: a
// RoundTripper must not change the request it is given.
type agent struct {
	next  http.RoundTripper
	value string
}

func (a agent) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") != "" {
		return a.next.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", a.value)
	return a.next.RoundTrip(r)
}

// Proxy is the proxy for req, nil for none, from the environment as it is
// now.
func Proxy(req *http.Request) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
}

// Set are the proxy variables set, by name, their passwords hidden: for
// infrapi-cli dev doctor.
func Set() [][2]string {
	var out [][2]string
	for _, name := range Vars {
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			continue
		}
		if !strings.HasPrefix(name, "NO_") && !strings.HasPrefix(name, "no_") {
			if u, err := url.Parse(v); err == nil && u.Host != "" {
				v = u.Redacted()
			}
		}
		out = append(out, [2]string{name, v})
	}
	return out
}
