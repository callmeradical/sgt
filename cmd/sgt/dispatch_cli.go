package main

// Four thin CLI wrappers over sgt ui's dispatch/create-PR/runs endpoints
// (docs/prd-cli-dispatch-subcommands.md; openspec/changes/cli-dispatch-subcommands).
//
// Each subcommand parses its own flags, resolves the target `sgt ui`
// address, calls exactly one internal/sgtclient function, and re-marshals
// that function's own response struct to stdout. None of them build an
// http.Request or json.Marshal a request/response shape of their own for
// these four endpoints — sgtclient is the only place that happens, per
// design.md's decision 4 / the Quality bar's structural constraint. A
// validation failure the HTTP handler already produces (unrecognized type,
// unknown change-id, non-green bullet, unreachable `sgt ui`) reaches stderr
// as sgtclient returned it, verbatim — never re-worded here.
import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/callmeradical/sgt/internal/sgtclient"
)

// resolveUIAddr resolves the running `sgt ui`'s base URL: SGT_UI_ADDR if
// set, sgtclient.DefaultAddr otherwise.
//
// This duplicates internal/mcp's resolveUIAddr (internal/mcp/server.go)
// rather than sharing it: cmd/sgt cannot import internal/mcp (wrong
// dependency direction), and the function is three lines, not worth a
// shared package of its own (design.md's explicit, accepted, intentional
// small duplication). Both copies must keep reading the same env var name
// and the same default.
func resolveUIAddr() string {
	if addr := os.Getenv("SGT_UI_ADDR"); addr != "" {
		return addr
	}
	return sgtclient.DefaultAddr
}

// repoFlags accumulates repeated `--repo` flags into an ordered []string —
// flag.Var's Value interface, not a comma-split string, so a repo name is
// never mis-split and `--repo svc --repo api` builds []string{"svc","api"}
// (design.md).
type repoFlags []string

func (r *repoFlags) String() string { return strings.Join(*r, ",") }

func (r *repoFlags) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// printJSONOrDie re-marshals v (the same struct sgtclient already decoded
// the endpoint's response into) to stdout. This is a re-rendering of that
// one struct, never a second independent shape.
func printJSONOrDie(v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding response: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

// dieOnErr writes err verbatim to stderr and exits 1. err is always
// sgtclient's own error (an HTTP handler's refusal text, or doJSON's
// unreachable-address message) — never rewritten here.
func dieOnErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// runDispatchCommand wraps sgtclient.Dispatch (POST /api/dispatch).
func runDispatchCommand(args []string) {
	fs := flag.NewFlagSet("dispatch", flag.ExitOnError)
	project := fs.String("project", "", "project name")
	brief := fs.String("brief", "", "one-line description of the work")
	var repos repoFlags
	fs.Var(&repos, "repo", "target repo name (repeatable: --repo svc --repo api)")
	agent := fs.String("agent", "", "agent to dispatch with")
	workType := fs.String("type", "", "work type: chore, docs, feat, fix, refactor, test")
	changeID := fs.String("change-id", "", "OpenSpec change id")
	requestID := fs.String("request-id", "", "idempotency key: a repeat returns the original run")
	_ = fs.Parse(args)

	req := sgtclient.DispatchRequest{
		Project:   *project,
		Brief:     *brief,
		Repos:     []string(repos),
		Agent:     *agent,
		Type:      *workType,
		ChangeID:  *changeID,
		RequestID: *requestID,
	}
	resp, err := sgtclient.Dispatch(resolveUIAddr(), req)
	if err != nil {
		dieOnErr(err)
	}
	printJSONOrDie(resp)
}

// runRunsCommand wraps sgtclient.Runs (GET /api/runs).
func runRunsCommand(args []string) {
	fs := flag.NewFlagSet("runs", flag.ExitOnError)
	project := fs.String("project", "", `scope to a project; empty or "all" combines every project`)
	_ = fs.Parse(args)

	out, err := sgtclient.Runs(resolveUIAddr(), *project)
	if err != nil {
		dieOnErr(err)
	}
	printJSONOrDie(out)
}

// runRunDetailsCommand wraps sgtclient.RunDetails (GET /api/run-details),
// taking the run id as a positional argument — the same convention `sgt
// run <project>` already uses in this file, rather than a --id flag.
func runRunDetailsCommand(args []string) {
	fs := flag.NewFlagSet("run-details", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: sgt run-details <run-id>")
		os.Exit(1)
	}
	runID := fs.Arg(0)

	out, err := sgtclient.RunDetails(resolveUIAddr(), runID)
	if err != nil {
		dieOnErr(err)
	}
	printJSONOrDie(out)
}

// runCreatePRCommand wraps sgtclient.CreatePR (POST /api/create-pr).
func runCreatePRCommand(args []string) {
	fs := flag.NewFlagSet("create-pr", flag.ExitOnError)
	runID := fs.String("run-id", "", "run id to seal and open a pull request for")
	project := fs.String("project", "", "project name")
	repo := fs.String("repo", "", "repo name within the project")
	title := fs.String("title", "", "pull request title")
	body := fs.String("body", "", "pull request body")
	_ = fs.Parse(args)

	req := sgtclient.CreatePRRequest{
		RunID:   *runID,
		Project: *project,
		Repo:    *repo,
		Title:   *title,
		Body:    *body,
	}
	resp, err := sgtclient.CreatePR(resolveUIAddr(), req)
	if err != nil {
		dieOnErr(err)
	}
	printJSONOrDie(resp)
}
