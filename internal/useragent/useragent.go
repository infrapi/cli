// Package useragent names infrapi-cli, and its version, in the User-Agent
// of every request it makes, infrapi-cli/<version>: the logs of Kayra, of
// an API or of APISIX then tell which client called, at which version,
// where Go's default, Go-http-client/1.1, tells nothing.
package useragent

import "net/http"

// Setup makes every request through http.DefaultTransport, the transport of
// each client of infrapi-cli, carry the User-Agent product/version, when it
// has none. Call it after netproxy.Setup, which changes the transport
// itself.
func Setup(product, version string) {
	http.DefaultTransport = Transport(product+"/"+version, http.DefaultTransport)
}

// Transport sets ua on a request that has no User-Agent, a clone of it: a
// RoundTripper must not change the request it is given.
func Transport(ua string, next http.RoundTripper) http.RoundTripper {
	return transport{ua: ua, next: next}
}

type transport struct {
	ua   string
	next http.RoundTripper
}

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") != "" {
		return t.next.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", t.ua)
	return t.next.RoundTrip(r)
}
