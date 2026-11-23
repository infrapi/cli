package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/infrapi/cli/internal/clicontext"
)

func TestMatchResource(t *testing.T) {
	groups := []discoveryGroup{
		{Api: "system.infrapi.io", Builtin: true, Resources: []discoveryResource{{Name: "namespaces"}}},
		{Api: "compute.infrapi.io", ShortName: "cp", Version: "v2", Resources: []discoveryResource{
			{Name: "vms", Namespaced: true}, {Name: "vms/reboot", Namespaced: true}, {Name: "policies", Namespaced: true}, {Name: "flavors"},
		}},
		{Api: "legacy.infrapi.io", Resources: []discoveryResource{{Name: "policies", Namespaced: true}}},
		{Api: "vault.infrapi.io"}, // registered, never synced
		// served in a namespace and cluster-scoped
		{Api: "events.infrapi.io", Builtin: true, Resources: []discoveryResource{{Name: "events", Namespaced: true}, {Name: "events"}}},
	}
	for typed, want := range map[string]resourceRef{
		"vms":                        {Api: "compute.infrapi.io", Version: "v2", Resource: "vms", Namespaced: true},
		"vm":                         {Api: "compute.infrapi.io", Version: "v2", Resource: "vms", Namespaced: true},
		"vms.compute.infrapi.io":     {Api: "compute.infrapi.io", Version: "v2", Resource: "vms", Namespaced: true},
		"vms.compute":                {Api: "compute.infrapi.io", Version: "v2", Resource: "vms", Namespaced: true},
		"vms.cp":                     {Api: "compute.infrapi.io", Version: "v2", Resource: "vms", Namespaced: true},
		"vms/reboot":                 {Api: "compute.infrapi.io", Version: "v2", Resource: "vms/reboot", Namespaced: true},
		"flavors":                    {Api: "compute.infrapi.io", Version: "v2", Resource: "flavors"},
		"namespaces":                 {Api: "system.infrapi.io", Version: "v1", Resource: "namespaces"},
		"policies.legacy.infrapi.io": {Api: "legacy.infrapi.io", Version: "v1", Resource: "policies", Namespaced: true},
		"secrets.vault.infrapi.io":   {Api: "vault.infrapi.io", Version: "v1", Resource: "secrets", Unknown: true},
		"things.other.infrapi.io":    {Api: "other.infrapi.io", Version: "v1", Resource: "things", Unknown: true},
		"events":                     {Api: "events.infrapi.io", Version: "v1", Resource: "events", Namespaced: true, Both: true},
	} {
		got, err := matchResource(groups, typed)
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v; want %+v", typed, got, err, want)
		}
	}
	_, err := matchResource(groups, "policies")
	var amb *errAmbiguous
	if !errors.As(err, &amb) || strings.Join(amb.fullNames, " ") != "policies.compute.infrapi.io policies.legacy.infrapi.io" {
		t.Errorf("ambiguous: %v", err)
	}
	for typed, msg := range map[string]string{
		"nope":                    "no api group serves",
		"nope.compute.infrapi.io": "serves no resource",
		"vms.nope":                "no api group named",
		"vms/stop":                `has no action "stop": reboot`,
		"flavors/reboot":          `has no action "reboot", nor any other`,
		"vms/":                    "one of its actions",
		"vms/reboot/now":          "one of its actions",
	} {
		if _, err := matchResource(groups, typed); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want %q", typed, err, msg)
		}
	}
}

func TestNamespaceFor(t *testing.T) {
	ns := resourceRef{Api: "compute.infrapi.io", Resource: "vms", Namespaced: true}
	cluster := resourceRef{Api: "system.infrapi.io", Resource: "namespaces"}
	ctx := clicontext.Context{Namespace: "thetenant"}
	for _, c := range []struct {
		ref        resourceRef
		flag, want string
		c          clicontext.Context
	}{
		{ns, "", "thetenant", ctx},
		{ns, "core", "core", ctx},
		{cluster, "core", "", ctx},
		{resourceRef{Unknown: true}, "", "thetenant", ctx},
	} {
		if got, err := namespaceFor(c.ref, c.flag, c.c); err != nil || got != c.want {
			t.Errorf("%+v %q: %q %v, want %q", c.ref, c.flag, got, err, c.want)
		}
	}
	if _, err := namespaceFor(ns, "", clicontext.Context{}); err == nil || !strings.Contains(err.Error(), "auth context namespace") {
		t.Errorf("no namespace: %v", err)
	}
}

func TestBuiltinGroup(t *testing.T) {
	doc := kayraDocument{Domain: "infrapi.io", APIs: map[string]string{
		"system":        "/apis/system.infrapi.io/v1",
		"catalog":       "/apis/catalog.infrapi.io/v1",
		"authorization": "/apis/authorization.infrapi.io/v1",
	}}
	if got := doc.builtinGroup("catalog"); got != "catalog.infrapi.io" {
		t.Errorf("builtinGroup(catalog) = %q", got)
	}
	if got := doc.builtinGroup("nope"); got != "" {
		t.Errorf("builtinGroup(nope) = %q", got)
	}
}

// A resource served both ways: in the namespace given, cluster-scoped
// without one.
func TestNamespaceForBoth(t *testing.T) {
	ref := resourceRef{Api: "events.infrapi.io", Resource: "events", Namespaced: true, Both: true}
	if ns, err := namespaceFor(ref, "core", clicontext.Context{}); err != nil || ns != "core" {
		t.Errorf("-n core: %q %v", ns, err)
	}
	if ns, err := namespaceFor(ref, "", clicontext.Context{}); err != nil || ns != "" {
		t.Errorf("no namespace: %q %v", ns, err)
	}
}
