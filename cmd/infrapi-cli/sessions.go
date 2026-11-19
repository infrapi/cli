package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/clicontext"
	"github.com/infrapi/cli/internal/ui"
)

// sessionsCommand lists and revokes the logins at Kayra's authorization
// server: infrapi-cli's, an MCP client's. A revoked one's access tokens
// are refused at once, its refresh token too.
func sessionsCommand() *cli.Command {
	user := &cli.StringFlag{Name: "user", Usage: "another user's: your cluster roles must grant list or delete on sessions"}
	return &cli.Command{
		Name:  "sessions",
		Usage: "list your logins at the server (infrapi-cli, MCP clients); revoke one, or every one",
		Flags: []cli.Flag{user},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runSessions(ctx, cmd, ui.Stdout())
		},
		Commands: []*cli.Command{{
			Name:      "revoke",
			Usage:     "revoke a login by its id, or every one with --all; the current one (*) logs this context out too",
			ArgsUsage: "<id> | --all",
			Flags:     []cli.Flag{user, &cli.BoolFlag{Name: "all", Usage: "every login of yours, or --user's"}},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return runRevokeSessions(ctx, cmd, ui.Stdout())
			},
		}},
	}
}

type session struct {
	ID       string    `json:"id"`
	UserID   string    `json:"user_id"`
	ClientID string    `json:"client_id"`
	Created  time.Time `json:"created_at"`
	LastUsed time.Time `json:"last_used_at"`
	Expires  time.Time `json:"expires_at"`
}

// sessionsCall sends method to the server's /oauth2/sessions[/id], ?user
// when given, with the context's token.
func sessionsCall(ctx context.Context, c clicontext.Context, method, id, user string) ([]byte, error) {
	u := strings.TrimRight(c.Server, "/") + "/oauth2/sessions"
	if id != "" {
		u += "/" + url.PathEscape(id)
	}
	if user != "" {
		u += "?user=" + url.QueryEscape(user)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var p struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(body, &p) == nil && p.Detail != "" {
			return nil, fmt.Errorf("%s %s: %d: %s", method, u, resp.StatusCode, p.Detail)
		}
		return nil, fmt.Errorf("%s %s: %d: %s", method, u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func runSessions(ctx context.Context, cmd *cli.Command, out io.Writer) error {
	c, err := resolveContext(cmd)
	if err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	body, err := sessionsCall(ctx, c, http.MethodGet, "", cmd.String("user"))
	if err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	var list []session
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	current := tokenSession(c.Token)
	rows := make([][]string, 0, len(list))
	for _, s := range list {
		mark := ""
		if s.ID == current {
			mark = "*"
		}
		rows = append(rows, []string{mark, s.ID, s.ClientID, s.UserID, ago(s.Created), ago(s.LastUsed), s.Expires.Local().Format(time.DateTime)})
	}
	return ui.Table(out, []string{"CURRENT", "ID", "CLIENT", "USER", "CREATED", "LAST USED", "EXPIRES"}, rows,
		func(i int) bool { return rows[i][0] == "*" })
}

func runRevokeSessions(ctx context.Context, cmd *cli.Command, out io.Writer) error {
	c, err := resolveContext(cmd)
	if err != nil {
		return fmt.Errorf("sessions revoke: %w", err)
	}
	all, id := cmd.Bool("all"), cmd.Args().First()
	switch {
	case all == (id != "") || cmd.Args().Len() > 1:
		return fmt.Errorf("sessions revoke: usage: infrapi-cli auth sessions revoke <id> | --all [--user <id>]")
	case all:
		body, err := sessionsCall(ctx, c, http.MethodDelete, "", cmd.String("user"))
		if err != nil {
			return fmt.Errorf("sessions revoke: %w", err)
		}
		var r struct {
			Revoked int `json:"revoked"`
		}
		_ = json.Unmarshal(body, &r)
		_, _ = fmt.Fprintf(out, "infrapi-cli: %s %d session(s)\n", ui.OK("revoked"), r.Revoked)
	default:
		if _, err := sessionsCall(ctx, c, http.MethodDelete, id, ""); err != nil {
			return fmt.Errorf("sessions revoke: %w", err)
		}
		_, _ = fmt.Fprintf(out, "infrapi-cli: %s session %s\n", ui.OK("revoked"), ui.Name(id))
	}
	return nil
}

// ago is how long ago t was, coarsely: 3m, 5h, 2d.
func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// tokenSession is the session (sid) a token of Kayra's authorization
// server is of, read without checking it: only to mark it in a list.
func tokenSession(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sid string `json:"sid"`
	}
	_ = json.Unmarshal(data, &claims)
	return claims.Sid
}
