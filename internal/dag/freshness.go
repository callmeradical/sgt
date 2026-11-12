package dag

import (
	"fmt"

	"github.com/callmeradical/sgt/internal/config"
	"github.com/callmeradical/sgt/internal/store"
)

// PhasePassed reports whether repo's phase or gate called name, of the given
// record kind ("agent" for pipeline phases, "code" for gates), counts as
// passed (design decision 8). It is the one rule behind resume's skips, the
// resume response's skipped list and the run details' resume_skips, for every
// harness.
//
//   - Lookups match name and kind, so a gate named "build" is never judged as
//     the agent build phase, nor the reverse.
//   - A work phase (plan, build, a custom agent phase) counts when its newest
//     record for repo passed.
//   - A verification — any gate, or the agent "review" phase — counts only
//     when its newest record passed and is newer than every code-changing
//     record of repo, of any status. The code-changing records are every
//     "fix" record, plus every record of a non-review agent phase that
//     pipeline places before the verification. Gates sit at the position of
//     "test". A verification whose position pipeline does not give is judged
//     against every non-review agent phase in pipeline, so it can never be
//     counted fresh on less evidence than a placed one.
//
// pipeline is repo's effective pipeline, from PipelineFor. Records are
// ordered by CreatedAt, ties broken by their position in phases, so the
// result does not depend on how the caller sorted them.
func PhasePassed(phases []store.PhaseRecord, repo, name, kind string, pipeline []string) bool {
	newest := -1
	for i := range phases {
		p := &phases[i]
		if p.Repo == repo && p.Name == name && p.Kind == kind && (newest < 0 || !recordBefore(phases, i, newest)) {
			newest = i
		}
	}
	if newest < 0 || phases[newest].Status != "passed" {
		return false
	}
	if !isVerification(name, kind) {
		return true
	}

	changing := codeChangingPhases(name, kind, pipeline)
	for i := range phases {
		p := &phases[i]
		if p.Repo != repo || p.Kind != "agent" {
			continue
		}
		if (p.Name == "fix" || changing[p.Name]) && !recordBefore(phases, i, newest) {
			return false
		}
	}
	return true
}

// isVerification reports whether a record of name and kind is judged against
// code changes: every gate, and the agent review phase.
func isVerification(name, kind string) bool {
	return kind == "code" || (kind == "agent" && name == "review")
}

// codeChangingPhases is the set of agent phase names whose records make a
// verification at its pipeline position stale, apart from "fix", which
// always does. review is never a code change: its prompt forbids edits.
func codeChangingPhases(name, kind string, pipeline []string) map[string]bool {
	at := "test"
	if kind == "agent" {
		at = name
	}
	pos := len(pipeline)
	for i, phase := range pipeline {
		if phase == at {
			pos = i
			break
		}
	}
	out := map[string]bool{}
	for _, phase := range pipeline[:pos] {
		if phase != "review" && phase != "test" {
			out[phase] = true
		}
	}
	return out
}

// recordBefore reports whether phases[i] was written before phases[j].
func recordBefore(phases []store.PhaseRecord, i, j int) bool {
	a, b := phases[i].CreatedAt, phases[j].CreatedAt
	if a.Equal(b) {
		return i < j
	}
	return a.Before(b)
}

// RunVerified is design decision 8's terminal guard: it reports whether a
// run may be recorded passed, and when it may not, the reason naming the
// phase and repo that stop it. Every entry point that ends a run passed
// (dispatch, resume, an operator fix cycle, sgt run) asks it first.
//
// repos are the repos the run is accountable for, in the order a reason
// should name them. For each, every phase of its effective pipeline
// (PipelineFor(repoCfg, roles)) must count as passed by PhasePassed:
//
//   - "fix" is never required;
//   - "test" is satisfied by the repo's configured gate records (kind
//     "code"), or by the placeholder "test" gate record when it configures
//     none, so gates are required only when the pipeline has "test";
//   - every other entry is the agent phase of that name.
//
// A DAG project's repo that is in none of its stages can never run, so it is
// reported first, with a reason that never says to resume. A repo with no
// records at all has not run. Otherwise the first phase that does not count
// is named, as a verification with no fresh pass or a work phase that did
// not pass; when the repo's newest "fix" failed, the reason also points at
// POST /api/run-fix.
func RunVerified(phases []store.PhaseRecord, proj *config.Project, repos []string, roles bool) (ok bool, reason string) {
	for _, repo := range repos {
		if !inSomeStage(proj, repo) {
			return false, fmt.Sprintf("repo %s has a bullet but is in no stage of this project's DAG; it cannot run", repo)
		}
	}
	for _, repo := range repos {
		if reason := repoUnverified(phases, proj, repo, roles); reason != "" {
			return false, reason
		}
	}
	return true, ""
}

// repoUnverified is RunVerified for one repo: "" when every required phase
// counts as passed, else the reason it does not.
func repoUnverified(phases []store.PhaseRecord, proj *config.Project, repo string, roles bool) string {
	ran := false
	for i := range phases {
		if phases[i].Repo == repo {
			ran = true
			break
		}
	}
	if !ran {
		return fmt.Sprintf("repo %s has not run", repo)
	}

	var repoCfg config.Repo
	if proj != nil {
		repoCfg = proj.Repos[repo]
	}
	pipeline := PipelineFor(repoCfg, roles)

	type required struct{ name, kind string }
	var reqs []required
	for _, phase := range pipeline {
		switch phase {
		case "fix":
		case "test":
			gates := SortedGateNames(repoCfg)
			if len(gates) == 0 {
				gates = []string{"test"}
			}
			for _, g := range gates {
				reqs = append(reqs, required{g, "code"})
			}
		default:
			reqs = append(reqs, required{phase, "agent"})
		}
	}

	for _, r := range reqs {
		if PhasePassed(phases, repo, r.name, r.kind, pipeline) {
			continue
		}
		reason := fmt.Sprintf("phase %s on %s did not pass", r.name, repo)
		if isVerification(r.name, r.kind) {
			reason = fmt.Sprintf("phase %s on %s has no passing result since the last code change", r.name, repo)
		}
		if newestFixFailed(phases, repo) {
			reason += " (start a corrective cycle with POST /api/run-fix)"
		}
		return reason
	}
	return ""
}

// inSomeStage reports whether repo can be run by proj: always for a project
// with no DAG stages (a dispatch builds its own stage over its repos), else
// only when some stage lists it.
func inSomeStage(proj *config.Project, repo string) bool {
	if proj == nil || proj.DAG == nil || len(proj.DAG.Stages) == 0 {
		return true
	}
	for _, stage := range proj.DAG.Stages {
		for _, r := range stage.Repos {
			if r == repo {
				return true
			}
		}
	}
	return false
}

// newestFixFailed reports whether repo's newest "fix" record failed.
func newestFixFailed(phases []store.PhaseRecord, repo string) bool {
	newest := -1
	for i := range phases {
		p := &phases[i]
		if p.Repo == repo && p.Name == "fix" && p.Kind == "agent" && (newest < 0 || !recordBefore(phases, i, newest)) {
			newest = i
		}
	}
	return newest >= 0 && phases[newest].Status == "failed"
}
