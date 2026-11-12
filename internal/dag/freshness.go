package dag

import "github.com/callmeradical/sgt/internal/store"

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
