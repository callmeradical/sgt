package ui

import (
	"log"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

// accountableRepos is the set of repos run must account for before it may be
// recorded passed (design decision 8), in the order a reason names them.
// proj is the project the run executes, as its caller loaded it, so the set
// is drawn from the same configuration the stages run from:
//
//  1. the repos of its intent's bullets, in bullet Position order;
//  2. otherwise, for a run with no intent (sgt run, or a run recorded before
//     intents existed), the repos of its project's DAG stages, in stage
//     order;
//  3. otherwise, the repos its phase records name (reposForRun).
//
// Repos that merely have phase records do not define the set, so a repo that
// never ran is still accounted for. Resume runs the same set, so "resume the
// run to continue" reaches every repo the guard waits on.
func (srv *Server) accountableRepos(run *store.RunRecord, proj *config.Project) []string {
	if run.IntentID != "" {
		bullets, err := srv.Store.ListBulletsForIntent(run.IntentID)
		if err != nil {
			log.Printf("sgt: loading the bullets of run %s to find its accountable repos: %v", run.ID, err)
		}
		if repos := uniqueRepos(bullets, func(b store.BulletRecord) []string { return []string{b.Repo} }); len(repos) > 0 {
			return repos
		}
	}
	if proj != nil && proj.DAG != nil {
		if repos := uniqueRepos(proj.DAG.Stages, func(s config.DAGStage) []string { return s.Repos }); len(repos) > 0 {
			return repos
		}
	}
	return srv.reposForRun(run.ID)
}

// uniqueRepos flattens the repos of items, keeping first-seen order and
// dropping empty names and repeats.
func uniqueRepos[T any](items []T, repos func(T) []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		for _, r := range repos(item) {
			if r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}
