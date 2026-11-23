package main

// A resource is named as kubectl names one: its plural (or singular),
// "vms", or its full name, plural then api group, "vms.compute.infrapi.io";
// an action on an item as Kayra's roles name it, "vms/reboot", addressed
// at .../vms/<name>/reboot.
// GET /apis says which group serves it, at which version, and whether it
// is namespaced: a namespaced one is addressed in -n, or the context's
// namespace (`auth context namespace`), a cluster-scoped one never is.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/infrapi/cli/internal/app"
	"github.com/infrapi/cli/internal/clicontext"
	"github.com/infrapi/cli/internal/ui"
)

// resourceRef is a resource resolved through discovery.
type resourceRef struct {
	Api      string // compute.infrapi.io
	Version  string // v1
	Resource string // vms, or an action, as Kayra's roles name it: vms/reboot
	// Namespaced says where it is addressed. Unknown is a resource of a
	// group named in full whose spec discovery doesn't have yet: it is
	// addressed in a namespace when one is given. Both is a resource its
	// group serves in a namespace and cluster-scoped, events say: in a
	// namespace when one is given, cluster-scoped otherwise.
	Namespaced, Unknown, Both bool
}

// FullName is the resource's full name, plural then api group.
func (r resourceRef) FullName() string { return r.Base() + "." + r.Api }

// Base is the resource without its action: vms for vms/reboot.
func (r resourceRef) Base() string {
	base, _, _ := strings.Cut(r.Resource, "/")
	return base
}

// Action is the action on an item, reboot for vms/reboot; empty for none.
func (r resourceRef) Action() string {
	_, action, _ := strings.Cut(r.Resource, "/")
	return action
}

// errAmbiguous is a resource name several api groups serve.
type errAmbiguous struct {
	name      string
	fullNames []string
}

func (e *errAmbiguous) Error() string {
	return fmt.Sprintf("%q is served by several api groups: give its full name, one of %s", e.name, strings.Join(e.fullNames, ", "))
}

// resolveResource resolves typed against c's server's GET /apis.
func resolveResource(ctx context.Context, c clicontext.Context, typed string) (resourceRef, error) {
	status, body, err := doVerbRequest(ctx, c, http.MethodGet, "/apis", nil)
	if err != nil {
		return resourceRef{}, fmt.Errorf("resolve %q: %w", typed, err)
	}
	if status != http.StatusOK {
		return resourceRef{}, fmt.Errorf("resolve %q: GET /apis returned %d", typed, status)
	}
	var groups []discoveryGroup
	if err := json.Unmarshal(body, &groups); err != nil {
		return resourceRef{}, fmt.Errorf("resolve %q: decode GET /apis: %w", typed, err)
	}
	return matchResource(groups, typed)
}

// matchResource is resolveResource's lookup. typed is <name>[.<group>]
// [/<action>]; <name> matches a resource's plural or singular, <group> a
// group's full name, short name or first label, and <action> one of the
// resource's actions discovery lists (vms/reboot).
func matchResource(groups []discoveryGroup, typed string) (resourceRef, error) {
	base, sub, _ := strings.Cut(typed, "/")
	name, group, _ := strings.Cut(base, ".")
	if name == "" {
		return resourceRef{}, errors.New("a resource is required")
	}
	if strings.Contains(typed, "/") && (sub == "" || strings.Contains(sub, "/")) {
		return resourceRef{}, fmt.Errorf("%q: a resource, or a resource and one of its actions, puppetreleases/promote", typed)
	}
	candidates := groups
	if group != "" {
		candidates = nil
		for _, g := range groups {
			if group == g.Api || !strings.Contains(group, ".") && (group == g.ShortName || group == strings.SplitN(g.Api, ".", 2)[0]) {
				candidates = append(candidates, g)
			}
		}
		if len(candidates) == 0 {
			if !strings.Contains(group, ".") {
				return resourceRef{}, fmt.Errorf("no api group named %q: list them with `infrapi-cli apis`, or give the full name", group)
			}
			// a group discovery doesn't know: taken as written
			return resourceRef{Api: group, Version: "v1", Resource: withSub(name, sub), Unknown: true}, nil
		}
	}

	var found []resourceRef
	var noAction error
	for _, g := range candidates {
		for _, r := range g.Resources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			if singular, _ := app.Names(r.Name, "", ""); name != r.Name && name != singular {
				continue
			}
			if sub != "" && !slices.ContainsFunc(g.Resources, func(a discoveryResource) bool { return a.Name == r.Name+"/"+sub }) {
				noAction = errNoAction(g, r.Name, sub)
				continue
			}
			version := g.Version
			if version == "" {
				version = "v1"
			}
			ref := resourceRef{Api: g.Api, Version: version, Resource: withSub(r.Name, sub), Namespaced: r.Namespaced}
			// the same resource of the same group, namespaced and not: one
			if i := slices.IndexFunc(found, func(f resourceRef) bool { return f.Api == ref.Api && f.Resource == ref.Resource }); i >= 0 {
				found[i].Namespaced, found[i].Both = true, true
				continue
			}
			found = append(found, ref)
		}
	}
	if len(found) == 0 && noAction != nil {
		return resourceRef{}, noAction
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		var names []string
		for _, r := range found {
			names = append(names, r.FullName())
		}
		slices.Sort(names)
		return resourceRef{}, &errAmbiguous{name: name, fullNames: names}
	case group != "" && len(candidates) == 1 && len(candidates[0].Resources) == 0:
		// a registered group not synced yet: no spec to check against
		g := candidates[0]
		return resourceRef{Api: g.Api, Version: cmp.Or(g.Version, "v1"), Resource: withSub(name, sub), Unknown: true}, nil
	case group != "":
		return resourceRef{}, fmt.Errorf("api group %s serves no resource %q: list its resources with `infrapi-cli apis --api-group %s`", candidates[0].Api, name, candidates[0].Api)
	}
	return resourceRef{}, fmt.Errorf("no api group serves a resource %q: list them with `infrapi-cli apis`", name)
}

// errNoAction is an action resource has not in g: its actions listed.
func errNoAction(g discoveryGroup, resource, action string) error {
	var actions []string
	for _, a := range g.Resources {
		if b, act, ok := strings.Cut(a.Name, "/"); ok && b == resource {
			actions = append(actions, act)
		}
	}
	if len(actions) == 0 {
		return fmt.Errorf("%s.%s has no action %q, nor any other", resource, g.Api, action)
	}
	return fmt.Errorf("%s.%s has no action %q: %s", resource, g.Api, action, strings.Join(actions, ", "))
}

func withSub(name, sub string) string {
	if sub == "" {
		return name
	}
	return name + "/" + sub
}

// namespaceFor is where ref is addressed: -n, else the context's
// namespace, for a namespaced resource; "" for a cluster-scoped one.
func namespaceFor(ref resourceRef, flag string, c clicontext.Context) (string, error) {
	ns := cmp.Or(flag, c.Namespace)
	switch {
	case ref.Unknown, ref.Both:
		return ns, nil
	case !ref.Namespaced:
		return "", nil
	case ns == "":
		return "", fmt.Errorf("%s is namespaced: give -n <namespace>, or set the context's with `infrapi-cli auth context namespace <namespace>`", ref.FullName())
	}
	return ns, nil
}

// warnAmbiguous prints, for an ambiguous resource name, the groups
// serving it and the command to run again with each full name.
func warnAmbiguous(err error) {
	var amb *errAmbiguous
	if !errors.As(err, &amb) {
		return
	}
	w := ui.Stderr()
	_, _ = fmt.Fprintf(w, "infrapi-cli: %s %q is served by several api groups: run the command again with its full name:\n", ui.Warn("warning:"), amb.name)
	for _, full := range amb.fullNames {
		args := slices.Clone(os.Args[1:])
		for i, a := range args {
			if a == amb.name || strings.HasPrefix(a, amb.name+"/") {
				args[i] = full + strings.TrimPrefix(a, amb.name)
				break
			}
		}
		_, _ = fmt.Fprintf(w, "  %s\n", ui.Cmd("infrapi-cli "+strings.Join(args, " ")))
	}
}
