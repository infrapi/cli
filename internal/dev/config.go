// Package dev is infrapi-cli's dev stack: dev.yaml, the files rendered from
// it, the stack's containers (Stack), an API's route through the gateway,
// and the scaffolding of a new stack (Init).
package dev

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		// Config is merged into the rendered kayra.yaml: a map key by key,
		// a list or a value replaced, null removing the key.
		Config yaml.Node `yaml:"config"`
		// Keys are signing key files of your own, relative to dev.yaml,
		// instead of the keys the render generates.
		Keys struct {
			Self  string `yaml:"self"`
			Local string `yaml:"local"`
		} `yaml:"keys"`
	} `yaml:"kayra"`

	Gateway struct {
		// Routes are merged into the gateway's own routes by id: a new id
		// is a new route, null removes one.
		Routes map[string]yaml.Node `yaml:"routes"`
	} `yaml:"gateway"`

	Compose struct {
		// Env is added to, or replaces in, the env compose reads.
		Env map[string]string `yaml:"env"`
	} `yaml:"compose"`

	Ports Ports `yaml:"ports"`

	SysadminGroups []string `yaml:"sysadmin_groups"`
	Users          []User   `yaml:"users"`

	Example struct {
		API  string `yaml:"api"`
		Port int    `yaml:"port"`
		// Env is added to, or replaces in, the example API's settings.
		Env map[string]string `yaml:"env"`
	} `yaml:"example"`

	// dir is dev.yaml's directory: kayra.keys are relative to it.
	dir string
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

var (
	dnsName  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`)
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
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
	c.dir = filepath.Dir(path)
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

	def(&c.Example.API, "compute")
	if !dnsName.MatchString(c.FullAPI(c.Example.API)) {
		return fmt.Errorf("example.api: %q: a lower case DNS label, or a full DNS name", c.Example.API)
	}
	return c.checkOverrides()
}

// checkOverrides checks dev.yaml's overrides of the rendered files.
func (c *Config) checkOverrides() error {
	if n := &c.Kayra.Config; n.Kind != 0 && !isNull(n) && n.Kind != yaml.MappingNode {
		return fmt.Errorf("kayra.config: a map, merged into kayra.yaml")
	}
	for id, n := range c.Gateway.Routes {
		if !routeID.MatchString(id) {
			return fmt.Errorf("gateway.routes.%s: a route id is lower case letters, digits, - and _", id)
		}
		if !isNull(&n) && n.Kind != yaml.MappingNode {
			return fmt.Errorf("gateway.routes.%s: a route, as APISIX's Admin API takes it, or null to remove it", id)
		}
	}
	for key, env := range map[string]map[string]string{"compose.env": c.Compose.Env, "example.env": c.Example.Env} {
		for k, v := range env {
			if !envName.MatchString(k) {
				return fmt.Errorf("%s.%s: an environment variable name", key, k)
			}
			if strings.ContainsAny(v, "\n\r") {
				return fmt.Errorf("%s.%s: one line", key, k)
			}
		}
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

// AdminKey is APISIX's admin key in the dev stack's apisix/config.yaml.
const AdminKey = "podman-admin-api-key"
