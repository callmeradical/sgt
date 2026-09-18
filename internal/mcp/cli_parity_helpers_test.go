// Package mcp_test: see dispatch_tools_test.go's package comment for why.
package mcp_test

// Shared helpers for dispatch_parity_test.go's CLI-subprocess leg: building
// the real sgt binary once, running it as a real subprocess against a
// fixture's real httptest.Server, and translating the same request-body
// maps the HTTP/postJSON leg already uses into sgt dispatch/create-pr's
// flags — so all three legs of a parity test start from one shared body,
// never three independently hand-typed field lists that could drift apart.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	cliParityBinaryOnce sync.Once
	cliParityBinaryPath string
	cliParityBinaryErr  error
)

// cliParityBinary builds cmd/sgt once per test binary run (cached across
// every parity test in this file) and returns the path to the resulting
// executable — the same "build and run the real binary as a subprocess"
// precedent cmd/sgt's own tests and internal/repopolicy's
// TestMiseInstallLinksWikiDigestAndBuildsSgt use, applied here since the
// three-way parity test needs the CLI leg to be the real compiled binary,
// not a reimplementation of it.
func cliParityBinary(t *testing.T) string {
	t.Helper()
	cliParityBinaryOnce.Do(func() {
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			cliParityBinaryErr = fmt.Errorf("runtime.Caller(0) failed, cannot locate the repository")
			return
		}
		// <root>/internal/mcp/cli_parity_helpers_test.go
		root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

		dir, err := os.MkdirTemp("", "sgt-cli-parity-*")
		if err != nil {
			cliParityBinaryErr = err
			return
		}
		bin := filepath.Join(dir, "sgt")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/sgt")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			cliParityBinaryErr = fmt.Errorf("building sgt: %w\n%s", err, out)
			return
		}
		cliParityBinaryPath = bin
	})
	if cliParityBinaryErr != nil {
		t.Fatalf("building sgt binary: %v", cliParityBinaryErr)
	}
	return cliParityBinaryPath
}

// runSgtCLI runs the compiled sgt binary as a real subprocess with
// SGT_UI_ADDR pointed at addr — exactly as an operator's shell would — and
// returns its stdout, stderr, and exit error (nil on success).
func runSgtCLI(t *testing.T, bin, addr string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "SGT_UI_ADDR="+addr)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// cliArgsForDispatch converts a dispatch request body — the same
// map[string]interface{} shape postJSON's callers already build for the
// HTTP leg — into sgt dispatch's flags, field for field.
func cliArgsForDispatch(body map[string]interface{}) []string {
	args := []string{"dispatch"}
	if v, ok := body["project"].(string); ok && v != "" {
		args = append(args, "--project", v)
	}
	if v, ok := body["brief"].(string); ok && v != "" {
		args = append(args, "--brief", v)
	}
	for _, r := range stringsFromAny(body["repos"]) {
		args = append(args, "--repo", r)
	}
	if v, ok := body["agent"].(string); ok && v != "" {
		args = append(args, "--agent", v)
	}
	if v, ok := body["type"].(string); ok && v != "" {
		args = append(args, "--type", v)
	}
	if v, ok := body["change_id"].(string); ok && v != "" {
		args = append(args, "--change-id", v)
	}
	if v, ok := body["request_id"].(string); ok && v != "" {
		args = append(args, "--request-id", v)
	}
	return args
}

// cliArgsForCreatePR converts a create-pr request body into sgt
// create-pr's flags, field for field.
func cliArgsForCreatePR(body map[string]interface{}) []string {
	args := []string{"create-pr"}
	if v, ok := body["run_id"].(string); ok && v != "" {
		args = append(args, "--run-id", v)
	}
	if v, ok := body["project"].(string); ok && v != "" {
		args = append(args, "--project", v)
	}
	if v, ok := body["repo"].(string); ok && v != "" {
		args = append(args, "--repo", v)
	}
	if v, ok := body["title"].(string); ok && v != "" {
		args = append(args, "--title", v)
	}
	if v, ok := body["body"].(string); ok && v != "" {
		args = append(args, "--body", v)
	}
	return args
}

// stringsFromAny reads body["repos"] whether it was built as []string or
// []interface{} (the two shapes this file's test bodies use), returning a
// plain []string either way.
func stringsFromAny(v interface{}) []string {
	switch repos := v.(type) {
	case []string:
		return repos
	case []interface{}:
		out := make([]string, 0, len(repos))
		for _, r := range repos {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
