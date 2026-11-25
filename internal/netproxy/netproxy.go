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
// API, the login, APISIX's Admin API. Setup makes it read the environment
// on each request, where net/http reads it once.
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

// Setup makes http.DefaultTransport take its proxy from the environment.
func Setup() {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		t.Proxy = Proxy
	}
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
