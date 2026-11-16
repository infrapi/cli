// Package dev is infrapi-cli's dev stack: dev.yaml, the files rendered from
// it, the kickstart of Kayra, an API's route through the gateway, and the
// scaffolding of a new stack (Init).
package dev

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is dev.yaml: the dev stack's settings, every file it runs with
// rendered from them.
type Config struct {
	Domain   string `yaml:"domain"`
	Platform string `yaml:"platform"`
	Region   string `yaml:"region"`
	Location string `yaml:"location"`

	Kayra struct {
		// Image Kayra and its reconciler run, unless Src is set.
		Image string `yaml:"image"`
		// Src is a Kayra checkout to build kayra-dev:local from.
		Src string `yaml:"src"`
	} `yaml:"kayra"`

	Ports Ports `yaml:"ports"`

	SysadminGroups []string `yaml:"sysadmin_groups"`
	Users          []User   `yaml:"users"`
	APIs           []API    `yaml:"apis"`
	Tenants        []Tenant `yaml:"tenants"`

	Example struct {
		API  string `yaml:"api"`
		Port int    `yaml:"port"`
	} `yaml:"example"`
}

// Ports are where the stack listens, on 127.0.0.1.
type Ports struct {
	Kayra      int `yaml:"kayra"`
	Gateway    int `yaml:"gateway"`
	GatewayTLS int `yaml:"gateway_tls"`
	Admin      int `yaml:"admin"`
	Prometheus int `yaml:"prometheus"`
	Control    int `yaml:"control"`
	Etcd       int `yaml:"etcd"`
}

// User is one user of Kayra's "local" provider, as its users file has it.
type User struct {
	ID         string            `yaml:"id"`
	Password   string            `yaml:"password,omitempty"`
	Attributes map[string]string `yaml:"attributes,omitempty"`
	Groups     []string          `yaml:"groups"`
}

// API is one API registration of the kickstart.
type API struct {
	Name    string `yaml:"name"`
	Owner   string `yaml:"owner"`
	Version string `yaml:"version"`
}

// Tenant is one tenant of the kickstart: a namespace, its admins' RBAC and
// its subscriptions.
type Tenant struct {
	Name   string   `yaml:"name"`
	Admins []string `yaml:"admins"`
	// Subscriptions is "all", or a list of APIs.
	Subscriptions Subscriptions `yaml:"subscriptions"`
}

// Subscriptions is a tenant's: every API (All), or those named.
type Subscriptions struct {
	All   bool
	Names []string
}

// UnmarshalYAML reads "all", or a list of API names.
func (s *Subscriptions) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if n.Value != "all" {
			return fmt.Errorf("line %d: subscriptions: %q, want \"all\" or a list of APIs", n.Line, n.Value)
		}
		s.All = true
		return nil
	}
	return n.Decode(&s.Names)
}

var (
	dnsName  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`)
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	version  = regexp.MustCompile(`^v[1-9][0-9]*((alpha|beta)[1-9][0-9]*)?$`)
)

// LoadConfig reads dev.yaml at path, refusing an unknown key, then sets the
// defaults and checks it.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- dev.yaml, the path its own user gives
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) { // empty: the defaults
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) check() error {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	defInt := func(p *int, v int) {
		if *p == 0 {
			*p = v
		}
	}
	def(&c.Domain, "infrapi.io")
	def(&c.Platform, "sandbox")
	def(&c.Region, "eu-west-1")
	def(&c.Location, "dc1")
	def(&c.Kayra.Image, "ghcr.io/infrapi/kayra:latest")
	defInt(&c.Ports.Kayra, 8081)
	defInt(&c.Ports.Gateway, 9080)
	defInt(&c.Ports.GatewayTLS, 9443)
	defInt(&c.Ports.Admin, 9180)
	defInt(&c.Ports.Prometheus, 9091)
	defInt(&c.Ports.Control, 9092)
	defInt(&c.Ports.Etcd, 2379)
	defInt(&c.Example.Port, 8080)
	if len(c.SysadminGroups) == 0 {
		c.SysadminGroups = []string{c.Platform + "-core-sysadmin"}
	}

	if !dnsName.MatchString(c.Domain) {
		return fmt.Errorf("domain %q: a lower case DNS name of two labels or more, like infrapi.io", c.Domain)
	}
	// infrapi/lib's INFRAPI_APP_PLATFORM takes these only
	switch c.Platform {
	case "sandbox", "preprod", "prod":
	default:
		return fmt.Errorf("platform %q: sandbox, preprod or prod, as infrapi/lib takes it", c.Platform)
	}
	for name, v := range map[string]string{"region": c.Region, "location": c.Location} {
		if !dnsLabel.MatchString(v) {
			return fmt.Errorf("%s %q: a lower case DNS label", name, v)
		}
	}
	seen := map[int]string{}
	for name, p := range map[string]int{
		"kayra": c.Ports.Kayra, "gateway": c.Ports.Gateway, "gateway_tls": c.Ports.GatewayTLS, "admin": c.Ports.Admin,
		"prometheus": c.Ports.Prometheus, "control": c.Ports.Control, "etcd": c.Ports.Etcd, "example.port": c.Example.Port,
	} {
		if p < 1 || p > 65535 {
			return fmt.Errorf("ports.%s: %d is not a port", name, p)
		}
		if other, ok := seen[p]; ok {
			return fmt.Errorf("ports: %s and %s are both %d", other, name, p)
		}
		seen[p] = name
	}

	users := map[string]bool{}
	for _, u := range c.Users {
		if u.ID == "" || users[u.ID] {
			return fmt.Errorf("users: an id is empty or used twice: %q", u.ID)
		}
		users[u.ID] = true
	}

	apis := map[string]bool{}
	for i := range c.APIs {
		a := &c.APIs[i]
		def(&a.Version, "v1")
		full := c.FullAPI(a.Name)
		if !dnsName.MatchString(full) {
			return fmt.Errorf("apis: %q: a lower case DNS label, or a full DNS name", a.Name)
		}
		if !version.MatchString(a.Version) {
			return fmt.Errorf("apis: %s: version %q is not like v1 or v2beta1", a.Name, a.Version)
		}
		if apis[full] {
			return fmt.Errorf("apis: %s twice", full)
		}
		apis[full] = true
	}

	tenants := map[string]bool{}
	for i := range c.Tenants {
		t := &c.Tenants[i]
		if !dnsLabel.MatchString(t.Name) || tenants[t.Name] {
			return fmt.Errorf("tenants: %q: a lower case DNS label, once", t.Name)
		}
		tenants[t.Name] = true
		if len(t.Admins) == 0 {
			return fmt.Errorf("tenants: %s: admins: a group at least", t.Name)
		}
		for _, n := range t.Subscriptions.Names {
			if !apis[c.FullAPI(n)] {
				return fmt.Errorf("tenants: %s: subscriptions: %s is none of apis", t.Name, n)
			}
		}
	}

	def(&c.Example.API, "compute")
	if c.Example.API != "" && len(c.APIs) > 0 && !apis[c.FullAPI(c.Example.API)] {
		return fmt.Errorf("example.api: %s is none of apis", c.Example.API)
	}
	return nil
}

// FullAPI is an API's full name: name itself when it has a dot,
// <name>.<domain> otherwise.
func (c *Config) FullAPI(name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return name + "." + c.Domain
}

// Builtin is one of Kayra's own groups: system, catalog or authorization.
func (c *Config) Builtin(name string) string {
	return name + "." + c.Domain
}

// Subscribed are the full names of the APIs tenant t subscribes to.
func (c *Config) Subscribed(t Tenant) []string {
	var out []string
	if t.Subscriptions.All {
		for _, a := range c.APIs {
			out = append(out, c.FullAPI(a.Name))
		}
		return out
	}
	for _, n := range t.Subscriptions.Names {
		out = append(out, c.FullAPI(n))
	}
	return out
}

// AdminKey is APISIX's admin key in the dev stack's apisix/config.yaml.
const AdminKey = "podman-admin-api-key"
