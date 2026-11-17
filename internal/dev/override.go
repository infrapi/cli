package dev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Every file rendered into .infrapi-dev/ takes overrides from dev.yaml, all
// merged the same way: a map key by key, a list or a value replaced as a
// whole, null removing the key.
//
//	kayra.yaml     kayra.config
//	routes/*.json  gateway.routes.<id>, a new id a new route, null removing one
//	example.env    example.env
//	env            compose.env
//	keys/*.pem     kayra.keys.self, kayra.keys.local: a key file of your own
//	apisix/config.yaml  gateway.config
//
// compose.yaml, compose.docker.yaml, apisix/routes.sh and kayra/Dockerfile
// are infrapi-cli's own: a compose.override.yaml beside dev.yaml is read
// after compose.yaml.

var (
	envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	routeID = regexp.MustCompile(`^[a-z0-9]([-a-z0-9_]*[a-z0-9])?$`)
)

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// mergeNode merges over into base, a mapping, in place.
func mergeNode(base, over *yaml.Node) {
	for i := 0; i+1 < len(over.Content); i += 2 {
		k, v := over.Content[i], over.Content[i+1]
		j := -1
		for b := 0; b+1 < len(base.Content); b += 2 {
			if base.Content[b].Value == k.Value {
				j = b
				break
			}
		}
		switch {
		case isNull(v) && j >= 0:
			base.Content = append(base.Content[:j], base.Content[j+2:]...)
		case isNull(v):
		case j >= 0 && base.Content[j+1].Kind == yaml.MappingNode && v.Kind == yaml.MappingNode:
			mergeNode(base.Content[j+1], v)
		case j >= 0:
			// the template's comment stays with the key it describes
			nv := *v
			if nv.LineComment == "" {
				nv.LineComment = base.Content[j+1].LineComment
			}
			base.Content[j+1] = &nv
		default:
			base.Content = append(base.Content, k, v)
		}
	}
}

// mergeAny merges over into base: maps key by key, a nil value removing
// the key, anything else replacing base.
func mergeAny(base, over any) any {
	bm, ok1 := base.(map[string]any)
	om, ok2 := over.(map[string]any)
	if !ok1 || !ok2 {
		return over
	}
	out := make(map[string]any, len(bm))
	for k, v := range bm {
		out[k] = v
	}
	for k, v := range om {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = mergeAny(out[k], v)
	}
	return out
}

// plain is v as JSON decodes it: map[string]any, []any, float64, string...
func plain(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(b, &out)
}

// stackRoutes are the gateway's routes, dev.yaml's gateway.routes merged
// in, and the ids it removes.
func stackRoutes(c *Config) (routes []route, removed []string, err error) {
	base, err := gatewayRoutes(c)
	if err != nil {
		return nil, nil, err
	}
	overrides := map[string]any{}
	for id, n := range c.Gateway.Routes {
		if isNull(&n) {
			removed = append(removed, id)
			continue
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, nil, fmt.Errorf("gateway.routes.%s: %w", id, err)
		}
		if v, err = plain(v); err != nil {
			return nil, nil, fmt.Errorf("gateway.routes.%s: %w", id, err)
		}
		overrides[id] = v
	}
	sort.Strings(removed)
	gone := map[string]bool{}
	for _, id := range removed {
		gone[id] = true
	}
	known := map[string]bool{}
	for _, r := range base {
		known[r.ID] = true
		if gone[r.ID] {
			continue
		}
		body, err := plain(r.Body)
		if err != nil {
			return nil, nil, err
		}
		if o, ok := overrides[r.ID]; ok {
			body = mergeAny(body, o)
		}
		m, _ := body.(map[string]any)
		routes = append(routes, route{ID: r.ID, Body: m})
	}
	var extra []string
	for id := range overrides {
		if !known[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		m, _ := mergeAny(map[string]any{}, overrides[id]).(map[string]any)
		routes = append(routes, route{ID: id, Body: m})
	}
	return routes, removed, nil
}

// withEnv is the KEY=value lines of base, over's keys replaced in place and
// the others added after, under a comment naming from.
func withEnv(base string, over map[string]string, from string) string {
	lines := strings.Split(strings.TrimRight(base, "\n"), "\n")
	done := map[string]bool{}
	for i, l := range lines {
		if strings.HasPrefix(l, "#") {
			continue
		}
		k, _, ok := strings.Cut(l, "=")
		if v, set := over[k]; ok && set {
			lines[i] = k + "=" + envValue(v)
			done[k] = true
		}
	}
	var extra []string
	for k := range over {
		if !done[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		lines = append(lines, "# dev.yaml's "+from)
		for _, k := range extra {
			lines = append(lines, k+"="+envValue(over[k]))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// envValue is v as an env file takes it: as it is, or double-quoted when
// it has a space, a quote or a #.
func envValue(v string) string {
	if v == "" || !strings.ContainsAny(v, " \t\"'#\\$`") {
		return v
	}
	return strconv.Quote(v)
}

// parseEnv is an env file's KEY=value pairs, unquoted.
func parseEnv(s string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil && strings.HasPrefix(v, `"`) {
			v = u
		}
		out[k] = v
	}
	return out
}

// keep is the dev.yaml snippet that would render the files as they are on
// disk: for each file the render would change, what it has that the render
// does not, under the dev.yaml key it comes from. Keys and new files are
// left out.
func keep(was, now map[string][]byte) map[string]any {
	out := map[string]any{}
	put := func(v any, path ...string) {
		m := out
		for _, p := range path[:len(path)-1] {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		m[path[len(path)-1]] = v
	}
	names := make([]string, 0, len(was))
	for name := range was {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		old, cur := was[name], now[name]
		switch {
		case name == "kayra.yaml" || name == "apisix/config.yaml":
			key := []string{"kayra", "config"}
			if name == "apisix/config.yaml" {
				key = []string{"gateway", "config"}
			}
			var o, n any
			if yaml.Unmarshal(old, &o) != nil || yaml.Unmarshal(cur, &n) != nil {
				continue
			}
			o, _ = plain(o)
			n, _ = plain(n)
			for path, v := range leaves(o, n, nil) {
				if s, ok := v.(string); ok && strings.Contains(s, "PRIVATE KEY") {
					continue
				}
				put(v, append(append([]string{}, key...), strings.Split(path, "\x00")...)...)
			}
		case name == "env" || name == "example.env":
			key := []string{"compose", "env"}
			if name == "example.env" {
				key = []string{"example", "env"}
			}
			o, n := parseEnv(string(old)), parseEnv(string(cur))
			for k, v := range o {
				if n[k] != v {
					put(v, append(key, k)...)
				}
			}
		case strings.HasPrefix(name, "routes/"):
			id := strings.TrimSuffix(strings.TrimPrefix(name, "routes/"), ".json")
			if len(id) > 3 && id[2] == '-' {
				id = id[3:]
			}
			var o, n any
			if json.Unmarshal(old, &o) != nil {
				continue
			}
			if cur == nil {
				put(o, "gateway", "routes", id)
				continue
			}
			if json.Unmarshal(cur, &n) != nil {
				continue
			}
			for path, v := range leaves(o, n, nil) {
				put(v, append([]string{"gateway", "routes", id}, strings.Split(path, "\x00")...)...)
			}
		}
	}
	return out
}

// leaves are the values of o that n does not have, by their path: a map
// compared key by key, nil for a key n has and o does not.
func leaves(o, n any, path []string) map[string]any {
	out := map[string]any{}
	add := func(p []string, v any) { out[strings.Join(p, "\x00")] = v }
	om, ok1 := o.(map[string]any)
	nm, ok2 := n.(map[string]any)
	if ok1 && ok2 {
		for k, v := range om {
			for p, lv := range leaves(v, nm[k], append(append([]string{}, path...), k)) {
				out[p] = lv
			}
		}
		for k := range nm {
			if _, ok := om[k]; !ok {
				add(append(append([]string{}, path...), k), nil)
			}
		}
		return out
	}
	if !reflect.DeepEqual(o, n) && len(path) > 0 {
		add(path, o)
	}
	return out
}

// keepYAML is keep as dev.yaml lines, indented by indent.
func keepYAML(k map[string]any, indent string) string {
	if len(k) == 0 {
		return ""
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(k); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	return indent + strings.Join(lines, "\n"+indent) + "\n"
}
