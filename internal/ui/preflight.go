package ui

// The gauntlet plugin preflight (design decision 5). Every entry point that
// starts or restarts work for a project whose gauntlet roles apply asks
// claude, before it writes anything, whether an enabled gauntlet plugin with
// both roles is installed where a headless fleet session will see it. A
// request it cannot satisfy is refused with a message naming what is missing
// and how to opt out; one that passes carries the probed version onto the
// engine, so the phase records name the plugin that actually ran.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/redact"
)

// ProbeFunc reports claude's installed plugins: the raw stdout of
// `claude plugin list --json`. When the claude executable cannot be found,
// the error wraps exec.ErrNotFound, so the preflight can name the missing
// harness rather than a missing plugin.
//
// It returns raw output, not parsed entries, so scope and role-file
// filtering stay in gauntletPreflight, where a stubbed probe still exercises
// them.
type ProbeFunc func(ctx context.Context) ([]byte, error)

// gauntletProbeTimeout bounds the default probe. It sits below sgtclient's
// 30s HTTP timeout, so a slow probe cannot time out a CLI caller while the
// server goes on to create the run.
const gauntletProbeTimeout = 10 * time.Second

// defaultGauntletProbe is the probe NewServer installs, and the one a Server
// with no GauntletProbe falls back to. It is a variable only so this
// package's tests can replace it once, for every server they build, with a
// stub that never runs the real claude.
var defaultGauntletProbe ProbeFunc = claudePluginList

// claudePluginList runs `claude plugin list --json` from PATH, bounded by
// gauntletProbeTimeout (and by ctx), and returns its stdout.
func claudePluginList(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gauntletProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "plugin", "list", "--json")
	// A killed claude can leave a child holding stdout open; without a wait
	// bound, Output would block on it long after the deadline.
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("claude plugin list --json did not finish: %w", ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("claude plugin list --json: %w: %s", err, redact.Text(msg))
		}
		return nil, fmt.Errorf("claude plugin list --json: %w", err)
	}
	return stdout.Bytes(), nil
}

// pluginListEntry is the part of one `claude plugin list --json` entry the
// preflight reads. Other fields (projectPath, projectEnabled, ...) are
// ignored.
type pluginListEntry struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	InstallPath string `json:"installPath"`
}

// parsePluginList decodes `claude plugin list --json` output. Anything that
// is not a JSON array of plugin objects is an error: the preflight then
// reports that it could not read the list, not that no plugin is installed.
func parsePluginList(raw []byte) ([]pluginListEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("output is not a JSON plugin list")
	}
	var entries []pluginListEntry
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("output is not a JSON plugin list: %w", err)
	}
	return entries, nil
}

// gauntletRoleFiles are the role definitions a gauntlet install must carry
// for the phases sgt runs as gauntlet:alpha and gauntlet:beta.
var gauntletRoleFiles = []string{
	filepath.Join("agents", "alpha.md"),
	filepath.Join("agents", "beta.md"),
}

// usableGauntletVersion returns the version of the first entry that a
// headless fleet session can run roles from: an enabled gauntlet@ plugin,
// installed at user or managed scope (a project or local install is keyed to
// the operator's checkout path and does not apply inside a fleet worktree),
// whose install carries both role files.
func usableGauntletVersion(entries []pluginListEntry) (string, bool) {
	for _, e := range entries {
		if !strings.HasPrefix(e.ID, "gauntlet@") || !e.Enabled {
			continue
		}
		if e.Scope != "user" && e.Scope != "managed" {
			continue
		}
		if !hasRoleFiles(e.InstallPath) {
			continue
		}
		return e.Version, true
	}
	return "", false
}

func hasRoleFiles(installPath string) bool {
	if installPath == "" {
		return false
	}
	for _, f := range gauntletRoleFiles {
		info, err := os.Stat(filepath.Join(installPath, f))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// preflightResult is a successful preflight's answer, carried from the entry
// point that ran it to the engine and fix cycles it starts. The zero value is
// "roles off".
type preflightResult struct {
	Roles   bool
	Version string
}

// gauntletPreflight decides whether a request for proj runs with gauntlet
// roles. When proj.GauntletRolesApply() is false it answers false, "", nil
// without probing. Otherwise it probes claude's plugin list and returns true
// and the plugin's version, or an error whose text is the refusal the
// caller sends back, before any write.
func (srv *Server) gauntletPreflight(ctx context.Context, proj *config.Project) (roles bool, version string, err error) {
	if !proj.GauntletRolesApply() {
		return false, "", nil
	}

	probe := srv.GauntletProbe
	if probe == nil {
		probe = defaultGauntletProbe
	}

	raw, err := probe(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, "", errors.New(`agent harness "claude" not found on PATH; install it or set defaults.agent in the project`)
		}
		return false, "", probeFailure(proj, err)
	}
	entries, err := parsePluginList(raw)
	if err != nil {
		return false, "", probeFailure(proj, err)
	}
	version, ok := usableGauntletVersion(entries)
	if !ok {
		return false, "", fmt.Errorf(
			"gauntlet roles are on for project %s but claude reports no enabled gauntlet plugin (install: /plugin install gauntlet@gauntlet), or set defaults.gauntlet_roles: false",
			proj.Name)
	}
	return true, version, nil
}

func probeFailure(proj *config.Project, err error) error {
	return fmt.Errorf("could not read claude's plugin list for project %s: %v; retry, or set defaults.gauntlet_roles: false", proj.Name, err)
}
