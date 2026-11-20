package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AppEnv is the dev stack's dotenv for the API, rendered by infrapi-cli
// dev: .infrapi-dev/app.env.
const AppEnv = ".infrapi-dev/app.env"

// Run runs the project in root against the dev stack, with its settings,
// .infrapi-dev/app.env: go run ./cmd/<name>, printed first with a +
// prefix. It returns when the API stops.
func Run(ctx context.Context, root string, out io.Writer) error {
	envFile := filepath.Join(root, AppEnv)
	env, err := readDotenv(envFile)
	if err != nil {
		return fmt.Errorf("app run: %w: infrapi-cli dev init renders it", err)
	}
	info, err := Describe(root)
	if err != nil {
		return err
	}
	args := []string{"go", "run", "./cmd/" + info.Name}
	_, _ = fmt.Fprintln(out, "+ "+strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //#nosec G204 -- go, the project's own command
	cmd.Dir = root
	// the library reads the file; the environment, for what reads it directly
	cmd.Env = append(append(os.Environ(), env...), "INFRAPI_CONFIG_DOTENV_FILE="+envFile)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, out
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("app run: %w", err)
	}
	return nil
}

// readDotenv reads KEY=value lines, a value quoted or not; # comments.
func readDotenv(path string) ([]string, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- the project's own file
	if err != nil {
		return nil, err
	}
	var env []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		k, v, ok := strings.Cut(l, "=")
		v = strings.TrimSpace(v)
		if l == "" || strings.HasPrefix(l, "#") || !ok {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		env = append(env, strings.TrimSpace(k)+"="+v)
	}
	return env, nil
}
