package main

import (
	"runtime/debug"
	"time"
)

// buildVersion is version, a release's, or for a build without one what
// Go recorded of it: the commit it was built from in a checkout, +dirty
// with changes not committed, and its time, dev (6cc12c7+dirty,
// 2026-11-20T14:30Z); else the module's version (go install ...@v0.3.0).
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if version != "dev" || !ok {
		return version
	}
	return describeBuild(info)
}

func describeBuild(info *debug.BuildInfo) string {
	var rev, at string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				at = t.UTC().Format("2006-01-02T15:04Z")
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		// go install ...@v0.3.0: no checkout, the module's version
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		return version
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "+dirty"
	}
	if at != "" {
		rev += ", " + at
	}
	return version + " (" + rev + ")"
}
