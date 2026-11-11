package ui

// The one helper that keeps every Server built in this package's tests away
// from the real `claude` binary (design decision 5, "Test fixtures").
//
// NewServer installs defaultGauntletProbe as Server.GauntletProbe, and a
// Server built as a bare literal falls back to it. TestMain replaces it,
// before any test runs, with a stub that reports an enabled user-scope
// gauntlet@gauntlet 0.1.0 whose installPath holds both role files. No
// existing NewServer call site changes. A test that is about the probe sets
// srv.GauntletProbe itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// stubGauntletVersion is what the package-wide default stub probe reports.
const stubGauntletVersion = "0.1.0"

func TestMain(m *testing.M) {
	install, err := os.MkdirTemp("", "sgt-ui-gauntlet-install-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating stub gauntlet install: %v\n", err)
		os.Exit(1)
	}
	if err := writeRoleFiles(install, "alpha", "beta"); err != nil {
		fmt.Fprintf(os.Stderr, "writing stub gauntlet role files: %v\n", err)
		os.Exit(1)
	}
	out, err := json.Marshal([]pluginEntryJSON{{
		ID: "gauntlet@gauntlet", Version: stubGauntletVersion, Scope: "user", Enabled: true, InstallPath: install,
	}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding stub plugin list: %v\n", err)
		os.Exit(1)
	}
	defaultGauntletProbe = func(context.Context) ([]byte, error) { return out, nil }

	code := m.Run()
	_ = os.RemoveAll(install)
	os.Exit(code)
}

// pluginEntryJSON is one entry of `claude plugin list --json` as a test
// writes it. It is deliberately separate from the production decoding type,
// so a test states the CLI's field names rather than borrowing sgt's.
type pluginEntryJSON struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	InstallPath string `json:"installPath"`
	ProjectPath string `json:"projectPath,omitempty"`
}

func writeRoleFiles(installPath string, roles ...string) error {
	agents := filepath.Join(installPath, "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		return err
	}
	for _, r := range roles {
		if err := os.WriteFile(filepath.Join(agents, r+".md"), []byte("# "+r+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// gauntletInstall creates a temp plugin install directory holding the named
// role files (e.g. "alpha", "beta") under agents/.
func gauntletInstall(t *testing.T, roles ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeRoleFiles(dir, roles...); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pluginListJSON(t *testing.T, entries ...pluginEntryJSON) []byte {
	t.Helper()
	if entries == nil {
		entries = []pluginEntryJSON{}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recordingProbe is a ProbeFunc stub that counts its invocations. Its answer
// can be changed between requests with set.
type recordingProbe struct {
	mu    sync.Mutex
	calls int
	out   []byte
	err   error
}

func (p *recordingProbe) probe(context.Context) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.out, p.err
}

func (p *recordingProbe) set(out []byte, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out, p.err = out, err
}

func (p *recordingProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}
