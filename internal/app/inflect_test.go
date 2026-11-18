package app

import "testing"

func TestNames(t *testing.T) {
	for _, tt := range []struct{ name, singular, plural, wantS, wantP string }{
		{"vm", "", "", "vm", "vms"},
		{"vms", "", "", "vm", "vms"},
		{"environment", "", "", "environment", "environments"},
		{"environments", "", "", "environment", "environments"},
		{"policy", "", "", "policy", "policies"},
		{"policies", "", "", "policy", "policies"},
		{"gateway", "", "", "gateway", "gateways"},
		{"class", "", "", "class", "classes"},
		{"classes", "", "", "class", "classes"},
		{"box", "", "", "box", "boxes"},
		{"status", "", "", "status", "statuses"},
		{"databases", "", "", "database", "databases"},
		{"load-balancers", "", "", "load-balancer", "load-balancers"},
		{"caches", "cache", "", "cache", "caches"},
		{"cache", "", "", "cache", "caches"},
		{"person", "", "people", "person", "people"},
		{"people", "person", "", "person", "people"},
		{"", "person", "people", "person", "people"},
		{"", "", "people", "people", "people"},
	} {
		s, p := Names(tt.name, tt.singular, tt.plural)
		if s != tt.wantS || p != tt.wantP {
			t.Errorf("Names(%q, %q, %q) = %q, %q, want %q, %q", tt.name, tt.singular, tt.plural, s, p, tt.wantS, tt.wantP)
		}
	}
}
