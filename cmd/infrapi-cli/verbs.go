// Top-level kubectl-style verbs — get, create, update, patch, delete
// — each a thin, generic REST client against any app's API, following the
// route convention every app on infrapi/lib shares. `get` with no
// trailing <name> lists the whole collection, exactly like `kubectl get`
// does — there is no separate `list` verb.
//
//	{server}/apis/<api>/<version>[/namespaces/<namespace>]/<resource>[/<name>[/<action>]]
//
// authenticated with the current context's own bearer token. <resource>
// is named as kubectl names one, "vms" or "vms.compute.infrapi.io" (see
// resolveResource): discovery gives its api group, version and scope. An
// action is named as Kayra's roles name it, "vms/reboot", on an item:
// update vms/reboot web is PUT .../vms/web/reboot, its body optional.
// --field-selector k=v,... adds query parameters; --raw <path> sends any
// path of the server, discovery skipped.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/infrapi/cli/internal/ui"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/clicontext"
)

// nameMode says whether a verb's trailing <name> (one item, not the
// whole collection) may, must, or can't appear.
type nameMode int

const (
	nameNone     nameMode = iota // create: always the collection, never one item
	nameOptional                 // get: list the collection when absent, that one item when given — kubectl's own `get` does the same
	nameRequired                 // update/patch/delete: always exactly one item
)

// verbSpec says what a verb needs beyond <api>/<resource>: whether a
// trailing <name> is allowed/required, and whether it takes a request
// body.
type verbSpec struct {
	name        nameMode
	requireBody bool
}

// verbCommands returns every supported verb, each wired to the same
// runVerb with its own HTTP method and verbSpec. All are plain GET
// except where noted — `get` mirrors Kayra's own collection vs. item
// routes (e.g. GET .../subscriptions vs. GET .../subscriptions/:api)
// by the same means `kubectl get` does: list the collection when no
// <name> is given, fetch that one item when it is.
func verbCommands() []*cli.Command {
	return []*cli.Command{
		verbCommand("get", http.MethodGet, verbSpec{name: nameOptional}),
		verbCommand("create", http.MethodPost, verbSpec{name: nameNone, requireBody: true}),
		verbCommand("update", http.MethodPut, verbSpec{name: nameRequired, requireBody: true}),
		verbCommand("patch", http.MethodPatch, verbSpec{name: nameRequired, requireBody: true}),
		verbCommand("delete", http.MethodDelete, verbSpec{name: nameRequired}),
	}
}

func verbCommand(verb, method string, spec verbSpec) *cli.Command {
	argsUsage := "<resource> | <resource>/<action> <name> | --raw <path>"
	switch spec.name {
	case nameOptional:
		argsUsage = "<resource>[/<action>] [<name>] | --raw <path>"
	case nameRequired:
		argsUsage = "<resource>[/<action>] <name> | --raw <path>"
	}
	usage := fmt.Sprintf("%s a resource under any app's own API (kubectl-style)", verb)
	flags := []cli.Flag{
		&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "the namespace of a namespaced resource (default: the context's, see `auth context namespace`); a cluster-scoped one has none"},
		&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: outputTable, Usage: "output format: table (namespace, name, created/updated at), or json / yaml (the full response)"},
		&cli.StringFlag{Name: "raw", Usage: "send this path of the server as it is, /apis/...[?query]: no discovery, no <resource>, no -n; a body optional"},
	}
	if verb == "get" {
		flags = append(flags, &cli.StringFlag{Name: "field-selector", Usage: "query parameters, key=value[,key=value...]: environment=acme_sandbox,state=running"})
	}
	if spec.requireBody {
		flags = append(flags,
			&cli.StringFlag{Name: "data", Aliases: []string{"d"}, Usage: "request body, as a raw JSON string"},
			&cli.StringFlag{Name: "filename", Aliases: []string{"f"}, Usage: "request body, read from a file (\"-\" for stdin)"},
		)
	}

	return &cli.Command{
		Name:      verb,
		Usage:     usage,
		ArgsUsage: argsUsage,
		Flags:     flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runVerb(ctx, cmd, verb, method, spec)
		},
	}
}

func runVerb(ctx context.Context, cmd *cli.Command, verb, method string, spec verbSpec) error {
	output := cmd.String("output")
	switch output {
	case outputTable, outputJSON, outputYAML:
	default:
		return fmt.Errorf("%s: --output must be %q, %q or %q, got %q", verb, outputTable, outputJSON, outputYAML, output)
	}
	c, err := resolveContext(cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	var body []byte
	if spec.requireBody {
		if body, err = readVerbBody(cmd); err != nil {
			return fmt.Errorf("%s: %w", verb, err)
		}
	}
	if raw := cmd.String("raw"); raw != "" {
		return runRaw(ctx, cmd, c, verb, method, raw, body)
	}

	args := cmd.Args()
	if args.Len() < 1 || args.Len() > 2 {
		return verbUsage(verb, spec)
	}
	typed := args.Get(0)
	name := args.Get(1)
	if strings.Contains(name, "/") {
		base, action, _ := strings.Cut(name, "/")
		return fmt.Errorf("%s: %q: a name is one path segment; an action goes with the resource: infrapi-cli %s %s/%s %s", verb, name, verb, typed, action, base)
	}
	ref, err := resolveResource(ctx, c, typed)
	if err != nil {
		warnAmbiguous(err)
		return fmt.Errorf("%s: %w", verb, err)
	}
	action := ref.Action()
	switch {
	case action != "" && name == "":
		return fmt.Errorf("%s: %s is an action on an item: infrapi-cli %s %s <name>", verb, ref.Resource, verb, typed)
	case action == "" && spec.name == nameRequired && name == "",
		action == "" && spec.name == nameNone && name != "":
		return verbUsage(verb, spec)
	}
	// an action's body is its API's to require: none sent when none given
	if spec.requireBody && action == "" && len(body) == 0 {
		return fmt.Errorf("%s: a request body is required (--data or --filename)", verb)
	}
	query, err := fieldSelector(cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	namespace, err := namespaceFor(ref, cmd.String("namespace"), c)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	path := fmt.Sprintf("/apis/%s/%s/%s", ref.Api, ref.Version, ref.Base())
	if namespace != "" {
		path = fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", ref.Api, ref.Version, namespace, ref.Base())
	}
	if name != "" {
		path += "/" + url.PathEscape(name)
	}
	if action != "" {
		path += "/" + action
	}
	if query != "" {
		path += "?" + query
	}

	status, respBody, err := doVerbRequest(ctx, c, method, path, body)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	if status < 200 || status >= 300 {
		failVerb(output, status, respBody)
	}
	return printVerbResponse(os.Stdout, os.Stderr, output, verb, ref.Base(), name, namespace, respBody)
}

func verbUsage(verb string, spec verbSpec) error {
	nameUsage := ""
	switch spec.name {
	case nameOptional:
		nameUsage = " [<name>]"
	case nameRequired:
		nameUsage = " <name>"
	}
	return fmt.Errorf("%s: usage: infrapi-cli %s <resource>[.<api group>]%s [-n <namespace>], infrapi-cli %s <resource>/<action> <name>, or infrapi-cli %s --raw <path>",
		verb, verb, nameUsage, verb, verb)
}

// failVerb prints a failed response, and exits 1.
func failVerb(output string, status int, body []byte) {
	switch output {
	case outputJSON:
		printJSON(os.Stdout, body)
	case outputYAML:
		printYAML(os.Stdout, body)
	default:
		renderProblem(os.Stderr, status, body)
	}
	os.Exit(1)
}

// fieldSelector is --field-selector as query parameters: kubectl's
// syntax, key=value,..., each one a parameter of the API's own.
func fieldSelector(cmd *cli.Command) (string, error) {
	if !cmd.IsSet("field-selector") {
		return "", nil
	}
	q := url.Values{}
	for _, kv := range strings.Split(cmd.String("field-selector"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		switch {
		case !ok || k == "":
			return "", fmt.Errorf("--field-selector %q: key=value[,key=value...]", cmd.String("field-selector"))
		case strings.HasSuffix(k, "!") || strings.HasPrefix(v, "="):
			return "", fmt.Errorf("--field-selector %q: only key=value, each a query parameter the API filters on", kv)
		}
		q.Add(k, v)
	}
	return q.Encode(), nil
}

// runRaw sends path, the server's, as it is: an API's operation the verbs
// don't address, its query included.
func runRaw(ctx context.Context, cmd *cli.Command, c clicontext.Context, verb, method, path string, body []byte) error {
	switch {
	case !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//"):
		return fmt.Errorf("%s: --raw %q: a path of the server, /apis/...: the token goes to the context's server only", verb, path)
	case cmd.Args().Len() > 0:
		return fmt.Errorf("%s: --raw takes no <resource> or <name>", verb)
	case cmd.IsSet("namespace") || cmd.IsSet("field-selector"):
		return fmt.Errorf("%s: --raw is the whole path: no -n, no --field-selector", verb)
	}
	if len(body) == 0 {
		body = nil
	}
	status, respBody, err := doVerbRequest(ctx, c, method, path, body)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	output := cmd.String("output")
	if status < 200 || status >= 300 {
		failVerb(output, status, respBody)
	}
	switch {
	case len(bytes.TrimSpace(respBody)) == 0:
		return nil
	case output == outputYAML:
		printYAML(os.Stdout, respBody)
	case json.Valid(respBody):
		printJSON(os.Stdout, respBody)
	default:
		_, err = os.Stdout.Write(respBody)
		return err
	}
	return nil
}

// printVerbResponse prints a successful response: the full body for -o
// json / yaml; otherwise the table (see renderTable), a one-line confirmation
// for a delete's empty body, and the pretty JSON for anything that
// isn't a named resource (so nothing is ever hidden).
func printVerbResponse(stdout, stderr io.Writer, output, verb, resource, name, namespace string, body []byte) error {
	switch output {
	case outputJSON:
		printJSON(stdout, body)
		return nil
	case outputYAML:
		printYAML(stdout, body)
		return nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if verb == "delete" {
			_, err := fmt.Fprintf(ui.Writer(stdout), "%s %s %s\n", resource, ui.Name(strconv.Quote(name)), ui.OK("deleted"))
			return err
		}
		return nil
	}
	ok, err := renderTable(stdout, stderr, body, namespace)
	if err != nil || ok {
		return err
	}
	printJSON(stdout, body)
	return nil
}

// readVerbBody resolves a verb's request body: --data verbatim,
// --filename read from disk ("-" for stdin), or neither — in which
// case runVerb reports the missing body itself. The two flags are
// mutually exclusive in practice (a caller naming both gets --data
// silently preferred) rather than cross-validated: this is a thin
// client with no reason to duplicate validation the server already
// does on the body itself.
func readVerbBody(cmd *cli.Command) ([]byte, error) {
	if data := cmd.String("data"); data != "" {
		return []byte(data), nil
	}
	if filename := cmd.String("filename"); filename != "" {
		if filename == "-" {
			return io.ReadAll(os.Stdin)
		}
		return os.ReadFile(filename) //#nosec G304 -- filename is this user's own --filename flag, never attacker input
	}
	return nil, nil
}

// doVerbRequest sends one request against c's own server, bearer
// -authenticated exactly like checkAuthorized's call to /check — the
// same context, the same trust, just a caller-named path instead of a
// fixed one.
func doVerbRequest(ctx context.Context, c clicontext.Context, method, path string, body []byte) (int, []byte, error) {
	reqURL := strings.TrimRight(c.Server, "/") + path

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("call %s: %w", reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}
