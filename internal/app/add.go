package app

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Resource is a resource infrapi-cli app add adds to a contract.
type Resource struct {
	// Name is its name, lower case, singular or plural: vm, or vms. The
	// other form is inferred from it, unless Singular or Plural sets it.
	Name string
	// Singular is its name, singular: vm. From Name when empty.
	Singular string
	// Plural is its paths': vms. From Name when empty.
	Plural string
	// Kind is its schema's, its Go type's: VM. From Singular when empty.
	Kind string
	// Cluster puts it outside the namespaces: /apis/<group>/<version>/<plural>.
	Cluster bool
	// Version is the contract's: the only one when empty.
	Version string
}

// Added is what Add did.
type Added struct {
	// Singular and Plural are the resource's names: vm, vms.
	Singular, Plural string
	// Contract is the contract changed, relative to the project.
	Contract string
	// Files are the Go files written, relative to the project.
	Files []string
	// Ops are the resource's operations.
	Ops []string
}

var resourceName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// Add adds r to the project in root: its paths and schema to its
// contract, then the Go code generated from it, and its operations,
// answering 501 Not Implemented, in internal/<name>/<plural>.go with a
// test. Its contract, its Go files, are the project's from then on: Add
// writes them once, and refuses a resource the contract has.
func Add(root string, r Resource) (Added, error) {
	r.Name, r.Plural = Names(r.Name, r.Singular, r.Plural)
	if !resourceName.MatchString(r.Name) {
		return Added{}, fmt.Errorf("app add: %q: a resource's name, lower case, like vm", r.Name)
	}
	if !resourceName.MatchString(r.Plural) {
		return Added{}, fmt.Errorf("app add: plural %q: lower case, like vms", r.Plural)
	}
	if r.Name == r.Plural {
		return Added{}, fmt.Errorf("app add: %q is both its singular and its plural: --singular or --plural", r.Name)
	}
	if r.Kind == "" {
		r.Kind = exported(r.Name)
	}
	if !goName.MatchString(r.Kind) {
		return Added{}, fmt.Errorf("app add: kind %q: a Go type's name, like VM", r.Kind)
	}
	contracts, err := readContracts(filepath.Join(root, "api"))
	if err != nil {
		return Added{}, err
	}
	if apis := byAPI(contracts); len(apis) > 1 {
		return Added{}, fmt.Errorf("app add: api/ holds %d APIs (%s): write the resource in its contract, then infrapi-cli app generate", len(apis), strings.Join(apiNames(apis), ", "))
	}
	c := contracts[len(contracts)-1]
	if r.Version != "" {
		c = nil
		for _, cc := range contracts {
			if cc.Version == r.Version {
				c = cc
			}
		}
		if c == nil {
			return Added{}, fmt.Errorf("app add: no contract of version %s in api/", r.Version)
		}
	} else if len(contracts) > 1 {
		return Added{}, fmt.Errorf("app add: %d contracts in api/: --version", len(contracts))
	}

	vars := map[string]any{
		"Root": c.Root(), "Name": r.Name, "Plural": r.Plural, "Kind": r.Kind, "Kinds": exported(r.Plural),
		"Namespaced": !r.Cluster, "A": article(r.Name),
	}
	collection := c.Root() + "/namespaces/{namespace}/" + r.Plural
	if r.Cluster {
		collection = c.Root() + "/" + r.Plural
	}
	if _, ok := c.doc.Paths[collection]; ok {
		return Added{}, fmt.Errorf("app add: %s has %s already", c.File, collection)
	}
	if _, ok := c.doc.Components.Schemas.values[r.Kind]; ok {
		return Added{}, fmt.Errorf("app add: %s has a schema %s already: --kind", c.File, r.Kind)
	}

	path := filepath.Join(root, "api", c.File)
	data, err := os.ReadFile(path) //#nosec G304 -- the project's own contract
	if err != nil {
		return Added{}, err
	}
	paths, err := execute(pathsSnippet, vars)
	if err != nil {
		return Added{}, err
	}
	schemas, err := execute(schemaSnippet, vars)
	if err != nil {
		return Added{}, err
	}
	if data, err = insert(data, []string{"paths"}, paths); err != nil {
		return Added{}, fmt.Errorf("%s: %w", c.File, err)
	}
	if data, err = insert(data, []string{"components", "schemas"}, schemas); err != nil {
		return Added{}, fmt.Errorf("%s: %w", c.File, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //#nosec G306 G703 -- a source file of the project, its contract's path
		return Added{}, err
	}
	added := Added{Singular: r.Name, Plural: r.Plural, Contract: filepath.Join("api", c.File)}

	g, err := Generate(root)
	if err != nil {
		return added, err
	}
	added.Files = g.Files
	var ops []op
	for _, o := range g.Ops {
		if o.Path == collection || o.Path == collection+"/{name}" {
			ops = append(ops, o)
			added.Ops = append(added.Ops, o.Name)
		}
	}
	module, err := modulePath(root)
	if err != nil {
		return added, err
	}
	file := strings.ReplaceAll(r.Plural, "-", "_")
	for name, src := range map[string]string{file + ".go": stubsFile(g.Name, module, r, ops), file + "_test.go": testFile(g.Name, r, ops)} {
		rel := filepath.Join("internal", g.Name, name)
		out, err := format.Source([]byte(src))
		if err != nil {
			return added, fmt.Errorf("%s: %w\n%s", rel, err, src)
		}
		f, err := os.OpenFile(filepath.Join(root, rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //#nosec G302 G304 -- a source file
		if errors.Is(err, fs.ErrExist) {
			return added, fmt.Errorf("app add: %s exists: its methods are yours to write", rel)
		}
		if err != nil {
			return added, err
		}
		if _, err := f.Write(out); err != nil {
			_ = f.Close()
			return added, err
		}
		if err := f.Close(); err != nil {
			return added, err
		}
		added.Files = append(added.Files, rel)
	}
	return added, nil
}

func execute(t *template.Template, vars map[string]any) (string, error) {
	var b bytes.Buffer
	err := t.Execute(&b, vars)
	return b.String(), err
}

var pathsSnippet = template.Must(template.New("paths").Delims("[[", "]]").Parse(`[[ $c := print .Root (or (and .Namespaced "/namespaces/{namespace}") "") "/" .Plural ]]# [[ .Plural ]]: infrapi-cli app add [[ .Name ]]
[[ $c ]]:
[[- if .Namespaced ]]
  parameters:
    - $ref: '#/components/parameters/namespace'
[[- end ]]
  get:
    operationId: list[[ .Kinds ]]
    summary: List the [[ .Plural ]][[ if .Namespaced ]] of a namespace[[ end ]].
    tags: [[ "[" ]][[ .Plural ]]]
    responses:
      '200':
        description: The [[ .Plural ]].
        content:
          application/json:
            schema:
              type: array
              items: {$ref: '#/components/schemas/[[ .Kind ]]'}
      default: {$ref: '#/components/responses/Problem'}
  post:
    operationId: create[[ .Kind ]]
    summary: Create [[ .A ]] [[ .Name ]].
    tags: [[ "[" ]][[ .Plural ]]]
    requestBody:
      required: true
      content:
        application/json:
          schema: {$ref: '#/components/schemas/[[ .Kind ]]'}
    responses:
      '201':
        description: The [[ .Name ]] created.
        content:
          application/json:
            schema: {$ref: '#/components/schemas/[[ .Kind ]]'}
      default: {$ref: '#/components/responses/Problem'}
[[ $c ]]/{name}:
  parameters:
[[- if .Namespaced ]]
    - $ref: '#/components/parameters/namespace'
[[- end ]]
    - $ref: '#/components/parameters/name'
  get:
    operationId: get[[ .Kind ]]
    summary: Get [[ .A ]] [[ .Name ]].
    tags: [[ "[" ]][[ .Plural ]]]
    responses:
      '200':
        description: The [[ .Name ]].
        content:
          application/json:
            schema: {$ref: '#/components/schemas/[[ .Kind ]]'}
      default: {$ref: '#/components/responses/Problem'}
  put:
    operationId: replace[[ .Kind ]]
    summary: Replace [[ .A ]] [[ .Name ]].
    tags: [[ "[" ]][[ .Plural ]]]
    requestBody:
      required: true
      content:
        application/json:
          schema: {$ref: '#/components/schemas/[[ .Kind ]]'}
    responses:
      '200':
        description: The [[ .Name ]] replaced.
        content:
          application/json:
            schema: {$ref: '#/components/schemas/[[ .Kind ]]'}
      default: {$ref: '#/components/responses/Problem'}
  delete:
    operationId: delete[[ .Kind ]]
    summary: Delete [[ .A ]] [[ .Name ]].
    tags: [[ "[" ]][[ .Plural ]]]
    responses:
      '204':
        description: The [[ .Name ]] deleted.
      default: {$ref: '#/components/responses/Problem'}
`))

var schemaSnippet = template.Must(template.New("schema").Delims("[[", "]]").Parse(`[[ .Kind ]]:
  type: object
  description: [[ if eq .A "an" ]]An[[ else ]]A[[ end ]] [[ .Name ]].
  required: [name]
  properties:
    name:
      type: string
      description: Its name, unique[[ if .Namespaced ]] in its namespace[[ end ]].
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
      maxLength: 63
    created_at:
      type: string
      format: date-time
      readOnly: true
      description: When it was created.
    # [[ .Name ]]'s own fields, then infrapi-cli app generate: a type each, string,
    # integer, number, boolean, array or object, required listed above.
    # Example:
    # size:
    #   type: string
    #   enum: [small, medium, large]
`))

// insert adds block, YAML lines at no indentation, as the last entries of
// the mapping data has at keys, keeping every other line as it is.
func insert(data []byte, keys []string, block string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a YAML map")
	}
	lines := strings.Split(string(data), "\n")
	end := len(lines) + 1 // the line of the first key after the mapping
	m := doc.Content[0]
	var key, value *yaml.Node
	for depth, k := range keys {
		i := find(m, k)
		if i < 0 {
			return nil, fmt.Errorf("no %s", strings.Join(keys[:depth+1], "."))
		}
		if i+2 < len(m.Content) {
			end = m.Content[i+2].Line
		}
		key, value = m.Content[i], m.Content[i+1]
		m = value
	}

	var at, indent int
	switch {
	case value.Kind == yaml.MappingNode && len(value.Content) > 0 && value.Style&yaml.FlowStyle == 0:
		indent = value.Content[0].Column - 1
		at = end - 1
		// before the blank lines and comments of the next key
		for at-1 >= key.Line {
			l := lines[at-1]
			trimmed := strings.TrimSpace(l)
			if trimmed != "" && (!strings.HasPrefix(trimmed, "#") || len(l)-len(strings.TrimLeft(l, " ")) >= indent) {
				break
			}
			at--
		}
	case (value.Kind == yaml.MappingNode && len(value.Content) == 0) || (value.Kind == yaml.ScalarNode && value.Tag == "!!null"):
		// "paths: {}" or "paths:": the entries go right under the key
		indent = key.Column - 1 + 2
		l := lines[key.Line-1]
		lines[key.Line-1] = l[:key.Column-1] + key.Value + ":"
		at = key.Line
	default:
		return nil, fmt.Errorf("%s: a map of entries", strings.Join(keys, "."))
	}
	var add []string
	for _, l := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		if l != "" {
			l = strings.Repeat(" ", indent) + l
		}
		add = append(add, l)
	}
	out := append(append(append([]string{}, lines[:at]...), add...), lines[at:]...)
	result := []byte(strings.Join(out, "\n"))
	var check yaml.Node
	if err := yaml.Unmarshal(result, &check); err != nil {
		return nil, fmt.Errorf("adding to %s: %w", strings.Join(keys, "."), err)
	}
	return result, nil
}

// find is the index of key k in mapping m, -1 when it has none.
func find(m *yaml.Node, k string) int {
	if m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return i
		}
	}
	return -1
}

// zero is the zero value of a Go type, as an expression.
func zero(t string) string {
	switch {
	case t == "", strings.HasPrefix(t, "[]"), strings.HasPrefix(t, "map["), strings.HasPrefix(t, "*"), t == "any", t == "io.ReadCloser":
		return "nil"
	case t == "string":
		return `""`
	case t == "bool":
		return "false"
	case strings.HasPrefix(t, "int"), strings.HasPrefix(t, "float"):
		return "0"
	}
	return t + "{}"
}

// stubsFile is internal/<name>/<plural>.go: r's operations, 501 each.
func stubsFile(name, module string, r Resource, ops []op) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"context\"\n", name)
	usesAPI := false
	for _, o := range ops {
		usesAPI = usesAPI || strings.Contains(o.Body+o.Result, "api.")
	}
	if usesAPI {
		fmt.Fprintf(&b, "\n\t%q\n", module+"/api")
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "// The %s: generated once by infrapi-cli app add %s, yours from then\n// on. Each operation answers 501 Not Implemented until written.\n\n", r.Plural, r.Name)
	for _, o := range ops {
		comment(&b, "", o.doc())
		fmt.Fprintf(&b, "func (s *Service) %s%s {\n", o.Name, o.signature())
		if o.Result == "" {
			fmt.Fprintf(&b, "\treturn notImplemented(%q)\n}\n\n", o.Name)
		} else {
			fmt.Fprintf(&b, "\treturn %s, notImplemented(%q)\n}\n\n", zero(o.Result), o.Name)
		}
	}
	return b.String()
}

// testFile is internal/<name>/<plural>_test.go: every operation of r's
// answers, 501 until written.
func testFile(name string, r Resource, ops []op) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"net/http\"\n\t\"net/http/httptest\"\n\t\"strings\"\n\t\"testing\"\n\n\t\"github.com/gin-gonic/gin\"\n)\n\n", name)
	fmt.Fprintf(&b, "// Every operation of the %s is routed, and answers 501 Not Implemented\n// until written: change the status as you write each one.\n", r.Plural)
	fmt.Fprintf(&b, "func Test%s(t *testing.T) {\n\tgin.SetMode(gin.TestMode)\n\tengine := gin.New()\n\tRegister(engine, New())\n\n", exported(r.Plural))
	b.WriteString("\tfor _, tc := range []struct {\n\t\tmethod, path, body string\n\t\twant               int\n\t}{\n")
	for _, o := range ops {
		p := regexp.MustCompile(`\{namespace\}`).ReplaceAllString(o.Path, "team-a")
		p = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(p, "test-1")
		body := ""
		if o.Body != "" {
			body = `{"name": "test-1"}`
		}
		fmt.Fprintf(&b, "\t\t{%s, %q, %q, http.StatusNotImplemented},\n", "http.Method"+methodName(o.Method), p, body)
	}
	b.WriteString("\t} {\n\t\tw := httptest.NewRecorder()\n\t\treq := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))\n")
	b.WriteString("\t\treq.Header.Set(\"Content-Type\", \"application/json\")\n\t\tengine.ServeHTTP(w, req)\n")
	b.WriteString("\t\tif w.Code != tc.want {\n\t\t\tt.Errorf(\"%s %s: %d, want %d: %s\", tc.method, tc.path, w.Code, tc.want, w.Body)\n\t\t}\n\t}\n}\n")
	return b.String()
}

// article is "an" before a vowel, "a" otherwise.
func article(word string) string {
	if strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

func methodName(m string) string {
	return m[:1] + strings.ToLower(m[1:])
}
