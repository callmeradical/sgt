package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/dag"
	"github.com/callmeradical/sgt/internal/export"
	"github.com/callmeradical/sgt/internal/handoff"
	"github.com/callmeradical/sgt/internal/manual"
	"github.com/callmeradical/sgt/internal/mcp"
	"github.com/callmeradical/sgt/internal/naming"
	"github.com/callmeradical/sgt/internal/store"
	"github.com/callmeradical/sgt/internal/ui"
	"github.com/callmeradical/sgt/internal/upgrademigrate"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: sgt run <project-name-or-file>\n")
			os.Exit(1)
		}
		runAutoMigration()
		runProject(os.Args[2])
	case "status":
		runAutoMigration()
		showStatus()
	case "ui":
		runAutoMigration()
		startUI()
	case "mcp":
		runAutoMigration()
		startMCP()
	case "migrate":
		runMigrateCommand()
	case "dispatch":
		runDispatchCommand(os.Args[2:])
	case "runs":
		runRunsCommand(os.Args[2:])
	case "run-details":
		runRunDetailsCommand(os.Args[2:])
	case "create-pr":
		runCreatePRCommand(os.Args[2:])
	case "version":
		fmt.Println("sgt v0.2.1 (Go Native Multi-Repo Software Factory Engine + Goose MCP Extension)")
	case "--help", "-h", "help":
		if len(os.Args) > 2 {
			printHelpTopic(strings.Join(os.Args[2:], " "))
		} else {
			printUsage()
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

// printUsage prints the manual's table of contents ahead of the existing
// subcommand list, so a bare `sgt help`/`sgt`/`sgt --help` shows more than a
// user who does not yet know a topic to ask for would otherwise see.
func printUsage() {
	fmt.Println("Sgt - Multi-Repo Software Factory Orchestrator")
	fmt.Println()
	printSectionTitles()
	fmt.Println("\nUsage:")
	fmt.Print(manual.CommandList())
}

// printSectionTitles prints the manual's section titles as a table of
// contents. Shared by printUsage (no-argument sgt help) and
// printHelpTopic's zero-match case, so a user always lands on the same list
// of "somewhere to go next."
func printSectionTitles() {
	fmt.Println("Manual sections (run `sgt help \"<title>\"` for one):")
	for _, s := range manual.Sections() {
		fmt.Println("  " + s.Title)
	}
}

// printHelpTopic answers `sgt help <query>` by searching the manual:
//   - no match: state plainly that the manual does not cover the query,
//     and list the available section titles instead of fabricating an
//     answer.
//   - one match: print that section's title and full body.
//   - two or more matches: print each matching title with a pointer to ask
//     again more specifically, rather than dumping every matched section's
//     full body at once.
func printHelpTopic(query string) {
	matches := manual.Search(query)
	switch len(matches) {
	case 0:
		fmt.Printf("The manual does not cover %q.\n\n", query)
		printSectionTitles()
	case 1:
		fmt.Printf("## %s\n\n%s\n", matches[0].Title, matches[0].Body)
	default:
		fmt.Printf("%q matches more than one section:\n\n", query)
		for _, s := range matches {
			fmt.Printf("  %s — run `sgt help %q` for the full section\n", s.Title, s.Title)
		}
	}
}

// runAutoMigration is called at the top of every subcommand that resolves
// config/store paths (run, status, ui, mcp — Decision 2 of
// docs/prd-upgrade-migration.md), before any of that subcommand's own
// logic, so none of them can ever start from an apparently empty
// config/store while recognizable pre-rebrand v2 state sits unmigrated on
// disk. upgrademigrate.Run() is itself cheap once migration has completed
// (a verified sentinel short-circuits to a single stat), so this call adds
// no meaningful cost to the common case.
//
// A migration error is never silently swallowed: it fails the invocation
// loudly, on stderr, with a non-zero exit, exactly like every other
// unrecoverable startup error in this file.
func runAutoMigration() {
	if _, err := upgrademigrate.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "upgrade migration: %v\n", err)
		os.Exit(1)
	}
}

// runMigrateCommand implements `sgt migrate` (Decision 7): the same
// automatic logic runAutoMigration calls, on demand, printing the
// resulting sentinel's status and any conflicts/mismatches rather than
// running silently ahead of some other subcommand's own output.
//
// Exit code: 0 when there was nothing to migrate, or migration reached
// Status "verified" with no conflicts; non-zero when Status is "failed" or
// any conflict was reported, so a script invoking this directly can tell
// "clean" from "needs operator attention" without parsing the sentinel
// itself.
func runMigrateCommand() {
	sentinel, err := upgrademigrate.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "upgrade migration: %v\n", err)
		os.Exit(1)
	}
	if sentinel == nil {
		fmt.Println("nothing to migrate")
		return
	}

	fmt.Printf("migration status: %s\n", sentinel.Status)
	if len(sentinel.Conflicts) > 0 {
		fmt.Println("conflicts:")
		for _, c := range sentinel.Conflicts {
			fmt.Printf("  - %s\n", c)
		}
	}
	if len(sentinel.Mismatches) > 0 {
		fmt.Println("mismatches:")
		for _, m := range sentinel.Mismatches {
			fmt.Printf("  - %s\n", m)
		}
	}
	if sentinel.Status == upgrademigrate.StatusFailed || len(sentinel.Conflicts) > 0 {
		os.Exit(1)
	}
}

func startMCP() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".local", "share", "sgt", "sgt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := mcp.NewMCPServer(st)
	if err := srv.ServeStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func runProject(projName string) {
	proj, err := config.LoadProject(projName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading project %s: %v\n", projName, err)
		os.Exit(1)
	}

	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".local", "share", "sgt", "sgt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// The same generator the dispatch handler uses. Two id formats would let a
	// CLI run and a dispatched run collide on the runs primary key.
	taskID := naming.RunID()
	// dag.FleetRoot is the single authority for the fleet root (D7). Building this
	// path by hand here is how the CLI kept writing handoffs into v1's directory
	// after the server stopped.
	handoffBase := filepath.Join(dag.FleetRoot(), taskID, "handoff")
	router := handoff.NewRouter(handoffBase)

	runRec := &store.RunRecord{
		ID:      taskID,
		Project: proj.Name,
		TaskID:  taskID,
		Status:  "running",
	}
	_ = st.CreateRun(runRec)

	fmt.Printf("🚀 Starting Multi-Repo Factory Run [%s] for project: %s\n", taskID, proj.Name)

	engine := dag.NewEngine(proj, st, router)
	ctx := context.Background()

	if proj.DAG != nil && len(proj.DAG.Stages) > 0 {
		for _, stage := range proj.DAG.Stages {
			fmt.Printf("\n▶ Executing Stage: %s (Repos: %v)\n", stage.Name, stage.Repos)
			if err := engine.RunStage(ctx, taskID, &stage); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Stage %s failed: %v\n", stage.Name, err)
				_ = st.UpdateRunStatus(taskID, "failed")
				os.Exit(1)
			}
			fmt.Printf("✔ Stage %s passed\n", stage.Name)
		}
	}

	_ = st.UpdateRunStatus(taskID, "passed")
	fmt.Printf("\n🎉 Factory Run [%s] completed successfully!\n", taskID)
}

func showStatus() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".local", "share", "sgt", "sgt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	runs, err := st.ListRecentRuns(10)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing runs: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tPROJECT\tSTATUS\tCREATED AT")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.ID, r.Project, r.Status, r.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	w.Flush()
}

func startUI() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".local", "share", "sgt", "sgt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	startExportRunners(st, export.Backends)

	server := ui.NewServer(st, 8484)
	if err := server.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting server: %v\n", err)
		os.Exit(1)
	}
}

// startExportRunners is the wiring point for internal/export.Runner: for
// each project with an Export block configured, it looks up
// proj.Export.Backend in backends. A hit constructs that backend's Target,
// builds a Runner, and starts it in its own goroutine alongside the HTTP
// server. A miss reports exactly as before this change — which backend name
// resolves to which Target is a separate, later decision, made by whatever
// registers into the map passed here (export.Backends in production).
func startExportRunners(st *store.Store, backends map[string]export.Constructor) {
	projects, err := config.ListProjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: listing projects: %v\n", err)
		return
	}
	for _, proj := range projects {
		if proj.Export == nil {
			continue
		}
		ctor, ok := backends[proj.Export.Backend]
		if !ok {
			fmt.Fprintf(os.Stderr, "export: project %q configures backend %q, but no export target implementation is registered yet; skipping\n", proj.Name, proj.Export.Backend)
			continue
		}
		target, err := ctor(*proj.Export)
		if err != nil {
			fmt.Fprintf(os.Stderr, "export: project %q backend %q: %v\n", proj.Name, proj.Export.Backend, err)
			continue
		}
		runner := &export.Runner{Store: st, Target: target}
		go func(projectName string) {
			if err := runner.Run(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "export: runner for project %q stopped: %v\n", projectName, err)
			}
		}(proj.Name)
	}
}
