package main

// Human-readable rendering of the generic verbs' responses (see
// verbs.go): a table of NAMESPACE (namespaced resources only), NAME,
// CREATED AT and UPDATED AT — the same style as `auth context list`. A
// resource without timestamps, an app's often, shows its first scalar
// fields instead (state, version...), in the order the API sent them. Use
// -o json or -o yaml for every field.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/infrapi/cli/internal/ui"
)

// Output formats accepted by --output.
const (
	outputTable = "table"
	outputJSON  = "json"
	outputYAML  = "yaml"
)

// nameKeys are the fields a resource's name is read from, first match
// wins: "name" for almost everything, "api" for a subscription (which is
// named by the api it subscribes to), "id" for what an API names itself
// (a rollout, say).
var nameKeys = []string{"name", "api", "id"}

// renderTable writes body as a table to stdout. namespace is the -n
// value: non-empty adds the NAMESPACE column. ok is false when body
// isn't a resource or a list of resources (no name to show), so the
// caller can fall back to JSON. An empty list prints nothing on stdout
// and a kubectl-style notice on stderr, so a script reading stdout sees
// no rows rather than a sentence.
func renderTable(stdout, stderr io.Writer, body []byte, namespace string) (ok bool, err error) {
	if evs, ok := eventRows(body); ok {
		return true, renderEvents(stdout, stderr, evs)
	}
	rows, ok := resourceRows(body)
	if !ok {
		return false, nil
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(stderr, "No resources found.")
		return true, err
	}

	header := []string{"NAME", "CREATED AT", "UPDATED AT"}
	cellsOf := func(row map[string]any) []string {
		return []string{rowName(row), timestampCell(row["created_at"]), timestampCell(row["updated_at"])}
	}
	if !anyTimestamp(rows) {
		keys := scalarKeys(body, rows)
		header = []string{"NAME"}
		for _, k := range keys {
			header = append(header, strings.ToUpper(strings.ReplaceAll(k, "_", " ")))
		}
		cellsOf = func(row map[string]any) []string {
			cell := []string{rowName(row)}
			for _, k := range keys {
				cell = append(cell, scalarCell(row[k]))
			}
			return cell
		}
	}
	if namespace != "" {
		header = append([]string{"NAMESPACE"}, header...)
	}
	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		cell := cellsOf(row)
		if namespace != "" {
			cell = append([]string{namespace}, cell...)
		}
		cells = append(cells, cell)
	}
	return true, ui.Table(stdout, header, cells, nil)
}

// maxScalarColumns caps the columns a resource without timestamps shows.
const maxScalarColumns = 5

func anyTimestamp(rows []map[string]any) bool {
	for _, row := range rows {
		if timestampCell(row["created_at"]) != "-" || timestampCell(row["updated_at"]) != "-" {
			return true
		}
	}
	return false
}

// scalarKeys are the columns of rows without timestamps: the top-level
// fields holding a string, number or boolean in some row, but the name,
// in the order the first object of body has them (a JSON object's order
// is the API's; a Go map has none), then any other row's; at most
// maxScalarColumns.
func scalarKeys(body []byte, rows []map[string]any) []string {
	name := ""
	for _, k := range nameKeys {
		if _, ok := rows[0][k].(string); ok {
			name = k
			break
		}
	}
	var keys []string
	seen := map[string]bool{name: true}
	add := func(k string) {
		if seen[k] || len(keys) == maxScalarColumns {
			return
		}
		for _, row := range rows {
			if scalarCell(row[k]) != "" {
				seen[k] = true
				keys = append(keys, k)
				return
			}
		}
	}
	for _, k := range objectKeys(body) {
		add(k)
	}
	for _, row := range rows {
		for k := range row {
			add(k)
		}
	}
	return keys
}

// objectKeys are the keys of body's first object, in order: body is an
// object, or a list of them.
func objectKeys(body []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t == json.Delim('[') {
		if t, err = dec.Token(); err != nil || t != json.Delim('{') {
			return nil
		}
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		k, _ := t.(string)
		keys = append(keys, k)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// scalarCell renders a string, number or boolean; "" for anything else
// (an object, a list, null), which never makes a column.
func scalarCell(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// resourceRows decodes body as a list of resources, or a single one as
// a one-row list. ok is false unless every row has a name.
func resourceRows(body []byte) (rows []map[string]any, ok bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, false
	}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, false
		}
	} else {
		var one map[string]any
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return nil, false
		}
		rows = []map[string]any{one}
	}
	for _, row := range rows {
		if rowName(row) == "" {
			return nil, false
		}
	}
	return rows, true
}

func rowName(row map[string]any) string {
	for _, k := range nameKeys {
		if s, ok := row[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// timestampCell renders an RFC 3339 timestamp in local time, like
// `auth context list`'s EXPIRY; "-" when absent (a record written before
// timestamps existed, or a resource that has none).
func timestampCell(v any) string {
	s, _ := v.(string)
	if s == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}

// renderProblem writes an error response readably to stderr: an
// infrapi/lib problem detail (every Kayra error, see internal/httpapi)
// as "Error: <title> (<status>): <detail>" plus its solution, anything
// else as the status and raw body.
func renderProblem(stderr io.Writer, status int, body []byte) {
	var p struct {
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		Solution string `json:"solution"`
	}
	out := ui.Writer(stderr)
	title := http.StatusText(status)
	if title == "" {
		title = "HTTP error"
	}
	if err := json.Unmarshal(body, &p); err == nil && p.Detail != "" {
		if p.Title != "" {
			title = p.Title
		}
		_, _ = fmt.Fprintf(out, "%s %s (%d): %s\n", ui.Fail("Error:"), ui.Bold(title), status, p.Detail)
		if p.Solution != "" {
			_, _ = fmt.Fprintf(out, "%s %s\n", ui.OK("Solution:"), p.Solution)
		}
		return
	}
	msg := fmt.Sprintf("%s %s (%d)", ui.Fail("Error:"), ui.Bold(title), status)
	if b := strings.TrimSpace(string(body)); b != "" {
		msg += ": " + b
	}
	_, _ = fmt.Fprintln(out, msg)
}

// printJSON pretty-prints body as JSON when it parses as such, falling
// back to the raw bytes verbatim otherwise.
func printJSON(w io.Writer, body []byte) {
	if len(body) == 0 {
		return
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err == nil {
		_ = ui.Highlight(w, "json", pretty.String()+"\n")
		return
	}
	_, _ = fmt.Fprintln(w, string(body))
}

// printYAML prints a JSON body as YAML, keeping the server's key order
// (JSON is valid YAML, so it parses straight into a yaml.Node; only its
// flow/quote styles are reset so it encodes as ordinary block YAML).
// A body that isn't JSON is printed verbatim.
func printYAML(w io.Writer, body []byte) {
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	var doc yaml.Node
	if !json.Valid(body) || yaml.Unmarshal(body, &doc) != nil {
		_, _ = fmt.Fprintln(w, string(body))
		return
	}
	clearStyle(&doc)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		_, _ = fmt.Fprintln(w, string(body))
		return
	}
	_ = enc.Close()
	_ = ui.Highlight(w, "yaml", out.String())
}

func clearStyle(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		clearStyle(c)
	}
}

// eventRow is the part of an event of events.<domain> its table shows.
type eventRow struct {
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Time       time.Time `json:"time"`
	Type       string    `json:"type"`
	Reason     string    `json:"reason"`
	Note       string    `json:"note"`
	Regarding  struct {
		Resource string `json:"resource"`
		Name     string `json:"name"`
	} `json:"regarding"`
	Controller string `json:"reporting_controller"`
	Series     *struct {
		Count        int       `json:"count"`
		LastObserved time.Time `json:"last_observed"`
	} `json:"series"`
}

// eventRows decodes body as events: a list, or one, each with an id, a
// reason and a reporting_controller.
func eventRows(body []byte) ([]eventRow, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, false
	}
	var evs []eventRow
	if trimmed[0] == '{' {
		trimmed = append(append([]byte{'['}, trimmed...), ']')
	}
	if json.Unmarshal(trimmed, &evs) != nil || len(evs) == 0 {
		return nil, false
	}
	for _, e := range evs {
		if e.ID == "" || e.Reason == "" || e.Controller == "" {
			return nil, false
		}
	}
	return evs, true
}

// renderEvents is kubectl get events' table: when last seen, its type,
// reason, object, count and note.
func renderEvents(stdout, _ io.Writer, evs []eventRow) error {
	now := time.Now()
	cells := make([][]string, 0, len(evs))
	for _, e := range evs {
		last, count := e.Time, 1
		if e.Series != nil {
			last, count = e.Series.LastObserved, e.Series.Count
		}
		if last.IsZero() {
			last = e.ReceivedAt
		}
		object := e.Regarding.Resource
		if e.Regarding.Name != "" {
			object += "/" + e.Regarding.Name
		}
		cells = append(cells, []string{age(now.Sub(last)), e.Type, e.Reason, object, strconv.Itoa(count), e.Note})
	}
	return ui.Table(stdout, []string{"LAST SEEN", "TYPE", "REASON", "OBJECT", "COUNT", "NOTE"}, cells, nil)
}

// age is d as kubectl shows it: 42s, 5m, 3h, 2d.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
