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
		runProject(os.Args[2])
	case "status":
		showStatus()
	case "ui":
		startUI()
	case "mcp":
		startMCP()
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
