// Package app is infrapi-cli app: a new API project (Init), a resource
// added to its contract (Add), the Go code generated from its contracts
// (Generate), and its registration in Kayra (Register).
//
// A project's contracts are api/<name>-<version>.yaml, OpenAPI 3.0
// documents: info.title is the API's name, every path is
// /apis/<group>/<version>/..., and every operation has an operationId.
// Generate writes, from them, api/zz_generated.go (the schemas as Go types,
// the contracts embedded) and internal/<name>/zz_generated.go (Handlers,
// one method per operation, and Register, its gin routes).
package app

import (
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// document is the part of an OpenAPI 3.0 document the generator reads.
type document struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Group       string `yaml:"x-infrapi-group"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	Paths      map[string]*pathItem `yaml:"paths"`
	Components struct {
		Schemas    ordered[*schema]      `yaml:"schemas"`
		Parameters map[string]*parameter `yaml:"parameters"`
	} `yaml:"components"`
}

type pathItem struct {
	Parameters []*parameter `yaml:"parameters"`
	Get        *operation   `yaml:"get"`
	Put        *operation   `yaml:"put"`
	Post       *operation   `yaml:"post"`
	Patch      *operation   `yaml:"patch"`
	Delete     *operation   `yaml:"delete"`
}

func (p *pathItem) operations() []struct {
	method string
	op     *operation
} {
	var out []struct {
		method string
		op     *operation
	}
	for _, m := range []struct {
		method string
		op     *operation
	}{{"GET", p.Get}, {"PUT", p.Put}, {"POST", p.Post}, {"PATCH", p.Patch}, {"DELETE", p.Delete}} {
		if m.op != nil {
			out = append(out, m)
		}
	}
	return out
}

type operation struct {
	OperationID string               `yaml:"operationId"`
	Summary     string               `yaml:"summary"`
	Parameters  []*parameter         `yaml:"parameters"`
	RequestBody *body                `yaml:"requestBody"`
	Responses   map[string]*response `yaml:"responses"`
}

type parameter struct {
	Ref    string  `yaml:"$ref"`
	Name   string  `yaml:"name"`
	In     string  `yaml:"in"`
	Schema *schema `yaml:"schema"`
}

type body struct {
	Content map[string]struct {
		Schema *schema `yaml:"schema"`
	} `yaml:"content"`
}

type response struct {
	Ref     string `yaml:"$ref"`
	Content map[string]struct {
		Schema *schema `yaml:"schema"`
	} `yaml:"content"`
}

type schema struct {
	Ref                  string            `yaml:"$ref"`
	Type                 string            `yaml:"type"`
	Format               string            `yaml:"format"`
	Description          string            `yaml:"description"`
	Items                *schema           `yaml:"items"`
	Properties           ordered[*schema]  `yaml:"properties"`
	Required             []string          `yaml:"required"`
	AdditionalProperties *additionalSchema `yaml:"additionalProperties"`
}

// additionalSchema is additionalProperties: a schema, or true or false.
type additionalSchema struct {
	schema *schema
	any    bool
}

func (a *additionalSchema) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&a.any)
	}
	a.schema = &schema{}
	return n.Decode(a.schema)
}

// ordered is a YAML mapping, in the order it is written.
type ordered[T any] struct {
	keys   []string
	values map[string]T
}

func (o *ordered[T]) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: a map", n.Line)
	}
	o.values = map[string]T{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		var v T
		if err := n.Content[i+1].Decode(&v); err != nil {
			return err
		}
		k := n.Content[i].Value
		o.keys = append(o.keys, k)
		o.values[k] = v
	}
	return nil
}

// contract is one api/<name>-<version>.yaml, read.
type contract struct {
	// File is its name, in api/.
	File    string
	Name    string
	Group   string
	Version string
	doc     *document
}

// Root is its paths' root: /apis/<group>/<version>.
func (c *contract) Root() string { return "/apis/" + c.Group + "/" + c.Version }

var (
	contractFile = regexp.MustCompile(`^([a-z][a-z0-9]*)-(v[0-9]+((alpha|beta)[0-9]+)?)\.yaml$`)
	goName       = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	apiPath      = regexp.MustCompile(`^/apis/([a-z0-9.-]+)/(v[0-9a-z]+)(/.*)?$`)
)

// readContracts reads the contracts in dir, api/: at least one, all of the
// same API and group.
func readContracts(dir string) ([]*contract, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*contract
	for _, e := range entries {
		m := contractFile.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		c, err := readContract(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if c.Name != m[1] {
			return nil, fmt.Errorf("%s: info.title is %q: the file is %s-%s.yaml", e.Name(), c.Name, c.Name, c.Version)
		}
		if len(out) > 0 && (c.Name != out[0].Name || c.Group != out[0].Group) {
			return nil, fmt.Errorf("%s: API %s, group %s: %s has %s, %s", e.Name(), c.Name, c.Group, out[0].File, out[0].Name, out[0].Group)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no contract, api/<name>-<version>.yaml", dir)
	}
	return out, nil
}

func readContract(path string) (*contract, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- the project's own contract
	if err != nil {
		return nil, err
	}
	var d document
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c := &contract{File: filepath.Base(path), Name: d.Info.Title, doc: &d}
	if !strings.HasPrefix(d.OpenAPI, "3.0") {
		return nil, fmt.Errorf("%s: openapi %q: 3.0.x", c.File, d.OpenAPI)
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9]*$`).MatchString(c.Name) {
		return nil, fmt.Errorf("%s: info.title %q: the API's name, its Go package: lower case letters and digits", c.File, c.Name)
	}
	c.Group = d.Info.Group
	_, c.Version, _ = strings.Cut(strings.TrimSuffix(c.File, ".yaml"), "-")
	if !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`).MatchString(c.Group) {
		return nil, fmt.Errorf("%s: info.x-infrapi-group %q: the API's group, a DNS name like compute.infrapi.io", c.File, c.Group)
	}
	for p := range d.Paths {
		m := apiPath.FindStringSubmatch(p)
		if m == nil || m[1] != c.Group || m[2] != c.Version {
			return nil, fmt.Errorf("%s: path %s: not under %s", c.File, p, c.Root())
		}
	}
	for _, name := range d.Components.Schemas.keys {
		if !goName.MatchString(name) {
			return nil, fmt.Errorf("%s: components.schemas.%s: a Go type's name, like VM", c.File, name)
		}
	}
	return c, nil
}

// op is one operation, as the generated code has it.
type op struct {
	Name    string // ListVMs
	Summary string
	Method  string // GET
	Path    string // the contract's: /apis/<group>/v1/namespaces/{namespace}/vms
	Params  []param
	Body    string // the request body's Go type, in package api; empty without one
	Status  int    // the success status
	Result  string // the response's Go type; empty without a body
	// Stream is the media type of a binary response (type: string,
	// format: binary), application/gzip say: Result is then io.ReadCloser,
	// streamed as it is.
	Stream string
}

type param struct {
	Name  string // as in the path or the query
	Ident string // as a Go parameter
	Query bool
}

// GinPath is Path with gin's :name for {name}.
func (o op) GinPath() string {
	return regexp.MustCompile(`\{([^}]+)\}`).ReplaceAllString(o.Path, ":$1")
}

// operations are c's operations, by path then method.
func (c *contract) operations(pkg string) ([]op, error) {
	paths := make([]string, 0, len(c.doc.Paths))
	for p := range c.doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []op
	for _, p := range paths {
		item := c.doc.Paths[p]
		for _, m := range item.operations() {
			o, err := c.operation(pkg, p, m.method, item, m.op)
			if err != nil {
				return nil, fmt.Errorf("%s: %s %s: %w", c.File, m.method, p, err)
			}
			out = append(out, o)
		}
	}
	return out, nil
}

func (c *contract) operation(pkg, path, method string, item *pathItem, o *operation) (op, error) {
	if o.OperationID == "" {
		return op{}, errors.New("no operationId: the Go method's name")
	}
	out := op{Name: exported(o.OperationID), Summary: o.Summary, Method: method, Path: path}
	if !goName.MatchString(out.Name) {
		return op{}, fmt.Errorf("operationId %q: a Go method's name", o.OperationID)
	}
	// the path's parameters, in the order the path has them, then the query's
	var query []param
	byName := map[string]bool{}
	for _, p := range append(append([]*parameter{}, item.Parameters...), o.Parameters...) {
		if p.Ref != "" {
			name := strings.TrimPrefix(p.Ref, "#/components/parameters/")
			ref, ok := c.doc.Components.Parameters[name]
			if !ok {
				return op{}, fmt.Errorf("parameter %s: not in components.parameters", p.Ref)
			}
			p = ref
		}
		if p.In == "query" && !byName[p.Name] {
			byName[p.Name] = true
			query = append(query, param{Name: p.Name, Ident: ident(p.Name), Query: true})
		}
	}
	for _, m := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
		out.Params = append(out.Params, param{Name: m[1], Ident: ident(m[1])})
	}
	out.Params = append(out.Params, query...)

	if o.RequestBody != nil {
		media, ok := o.RequestBody.Content["application/json"]
		if !ok || media.Schema == nil {
			return op{}, errors.New("requestBody: application/json only")
		}
		t, err := goType(pkg, media.Schema)
		if err != nil {
			return op{}, fmt.Errorf("requestBody: %w", err)
		}
		out.Body = t
	}

	codes := make([]string, 0, len(o.Responses))
	for code := range o.Responses {
		if len(code) == 3 && code[0] == '2' {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		return op{}, errors.New("no 2xx response")
	}
	_, _ = fmt.Sscan(codes[0], &out.Status)
	if r := o.Responses[codes[0]]; r.Ref == "" {
		if media, ok := r.Content["application/json"]; ok && media.Schema != nil {
			t, err := goType(pkg, media.Schema)
			if err != nil {
				return op{}, fmt.Errorf("response %s: %w", codes[0], err)
			}
			out.Result = t
		}
		for mt, media := range r.Content {
			if mt == "application/json" || out.Result != "" {
				continue
			}
			if media.Schema == nil || media.Schema.Type != "string" || media.Schema.Format != "binary" {
				return op{}, fmt.Errorf("response %s: %s: application/json, or type: string, format: binary", codes[0], mt)
			}
			out.Result, out.Stream = "io.ReadCloser", mt
		}
	}
	return out, nil
}

// goType is s as a Go type, its schemas' types in package pkg ("api." in
// another package, "" in api itself). An inline object is a map.
func goType(pkg string, s *schema) (string, error) {
	if s.Ref != "" {
		name, ok := strings.CutPrefix(s.Ref, "#/components/schemas/")
		if !ok {
			return "", fmt.Errorf("$ref %s: #/components/schemas/<name> only", s.Ref)
		}
		return pkg + name, nil
	}
	switch s.Type {
	case "string":
		switch s.Format {
		case "date-time":
			return "time.Time", nil
		case "byte":
			return "[]byte", nil
		}
		return "string", nil
	case "integer":
		if s.Format == "int32" {
			return "int32", nil
		}
		return "int64", nil
	case "number":
		if s.Format == "float" {
			return "float32", nil
		}
		return "float64", nil
	case "boolean":
		return "bool", nil
	case "array":
		if s.Items == nil {
			return "[]any", nil
		}
		t, err := goType(pkg, s.Items)
		return "[]" + t, err
	case "object", "":
		if s.AdditionalProperties != nil && s.AdditionalProperties.schema != nil {
			t, err := goType(pkg, s.AdditionalProperties.schema)
			return "map[string]" + t, err
		}
		return "map[string]any", nil
	}
	return "", fmt.Errorf("type %q", s.Type)
}

// initialisms are written in capitals in a Go name.
var initialisms = map[string]bool{
	"api": true, "cpu": true, "dns": true, "gb": true, "http": true, "https": true, "id": true, "ip": true, "json": true,
	"kb": true, "mb": true, "os": true, "sha": true, "sql": true, "ssh": true, "tcp": true, "tls": true, "ttl": true, "udp": true,
	"uri": true, "url": true, "uuid": true, "vm": true,
}

// exported is a JSON field or an operationId as an exported Go name:
// memory_mb is MemoryMB, listVMs ListVMs.
func exported(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		lw := strings.ToLower(w)
		switch {
		case strings.HasSuffix(lw, "s") && initialisms[lw[:len(lw)-1]]:
			b.WriteString(strings.ToUpper(lw[:len(lw)-1]) + "s") // VMs
		case initialisms[lw]:
			b.WriteString(strings.ToUpper(lw))
		default:
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

// words splits on _, -, . and before an upper case letter that follows a
// lower case one: listVMs is list, VMs.
func words(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == '.' || r == ' ' }) {
		start := 0
		for i := 1; i < len(part); i++ {
			if part[i] >= 'A' && part[i] <= 'Z' && part[i-1] >= 'a' && part[i-1] <= 'z' {
				out = append(out, part[start:i])
				start = i
			}
		}
		out = append(out, part[start:])
	}
	return out
}

// ident is a parameter's name as a Go identifier: namespace, vmName.
func ident(s string) string {
	e := exported(s)
	if e == "" {
		return "p"
	}
	w := words(s)[0]
	if lw := strings.ToLower(w); initialisms[lw] || initialisms[strings.TrimSuffix(lw, "s")] {
		e = lw + e[len(w):]
	} else {
		e = strings.ToLower(e[:1]) + e[1:]
	}
	if token.IsKeyword(e) || e == "ctx" || e == "body" {
		e += "Param"
	}
	return e
}

// Info is a project's API, as its contracts have it.
type Info struct {
	Name        string
	Group       string
	Description string
	// Versions are its contracts', in their files' order.
	Versions []string
}

// Describe reads the contracts of the project in root.
func Describe(root string) (Info, error) {
	contracts, err := readContracts(filepath.Join(root, "api"))
	if err != nil {
		return Info{}, err
	}
	in := Info{Name: contracts[0].Name, Group: contracts[0].Group, Description: contracts[0].doc.Info.Description}
	for _, c := range contracts {
		in.Versions = append(in.Versions, c.Version)
	}
	return in, nil
}
