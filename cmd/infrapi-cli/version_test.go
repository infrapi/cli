package main

import (
	"runtime/debug"
	"testing"
)

func TestDescribeBuild(t *testing.T) {
	vcs := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "6cc12c7a1b2c3d4e5f60718293a4b5c6d7e8f901"},
		{Key: "vcs.time", Value: "2026-11-20T14:30:12Z"},
		{Key: "vcs.modified", Value: "true"},
	}
	for _, c := range []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"go build in a checkout", debug.BuildInfo{Main: debug.Module{Version: "v0.2.1-0.20261120143012-6cc12c7a1b2c+dirty"}, Settings: vcs}, "dev (6cc12c7+dirty, 2026-11-20T14:30Z)"},
		{"clean", debug.BuildInfo{Settings: vcs[:2]}, "dev (6cc12c7, 2026-11-20T14:30Z)"},
		{"go install @version", debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "v0.3.0"},
		{"nothing recorded", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
	} {
		if got := describeBuild(&c.info); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
