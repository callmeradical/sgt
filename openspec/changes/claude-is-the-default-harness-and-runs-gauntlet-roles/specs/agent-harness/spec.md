# Agent harness

## ADDED Requirements

### Requirement: claude's background-wait ceiling is disabled

The runner SHALL invoke `claude` with `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0`
in its environment, overriding any inherited value, so that the phase's own
agent budget is the only limit on how long the session runs.

#### Scenario: The claude command carries an unbounded background-wait ceiling

- **WHEN** `BuildAgentCommand` is called with agent `claude`
- **THEN** the returned environment contains
  `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0`

#### Scenario: An inherited ceiling is overridden

- **WHEN** the sgt process environment has
  `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=600000` and a `claude` agent phase runs
- **THEN** the agent process observes the value `0`

### Requirement: claude sessions share no auto memory

The runner SHALL invoke `claude` with `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` in
its environment, overriding any inherited value, and with
`--no-session-persistence`.

#### Scenario: The claude command disables auto memory

- **WHEN** `BuildAgentCommand` is called with agent `claude`
- **THEN** the returned environment contains
  `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, and the arguments contain
  `--no-session-persistence`

#### Scenario: An inherited auto-memory setting is overridden

- **WHEN** the sgt process environment has `CLAUDE_CODE_DISABLE_AUTO_MEMORY=0`
  and a `claude` agent phase runs
- **THEN** the agent process observes the value `1`

### Requirement: A cut-off claude session never records a passed phase

A `claude` attempt SHALL be recorded `failed`, with a failure reason that
names the termination, even when the process exited 0, when one of the last
three non-empty lines of its output begins with the CLI's termination
notice, in either of its known wordings:
`Background tasks still running after <N>s; terminating.` or (claude
2.1.322) `Background tasks still running <duration> after the last turn
(<tasks>); stopping them.` The line may continue after that text.

#### Scenario: A zero exit with the termination notice is a failed phase

- **WHEN** a `claude` agent attempt exits 0 and its final output line is
  `Background tasks still running after 600s; terminating. Set CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0 to wait indefinitely.`
- **THEN** that attempt's phase record has status `failed`, its error names the
  background-task termination, and `RunAgentPhase` returns an error

#### Scenario: claude 2.1.322's notice on stderr is a failed phase

- **WHEN** a `claude` agent attempt exits 0 and writes to stderr
  `Background tasks still running 10m after the last turn (agent "zeta"); stopping them. Set CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=0 to wait indefinitely.`
- **THEN** that attempt's phase record has status `failed` and its error
  names the background-task termination

#### Scenario: A quoted notice mid-output is not a termination

- **WHEN** a `claude` attempt exits 0, its output contains
  `Background tasks still running after 600s; terminating.` inside a
  paragraph or code block, and is followed by more than three further
  non-empty lines
- **THEN** the attempt is recorded `passed`

#### Scenario: Other harnesses are not scanned for the notice

- **WHEN** a `goose` agent attempt exits 0 and its output contains the same
  termination text
- **THEN** the attempt is recorded `passed`, exactly as before this change

### Requirement: An unnamed harness resolves to claude

When neither the dispatch request nor the project's `defaults.agent` names a
harness, sgt SHALL run `claude`. A harness named explicitly SHALL run exactly
as before this change.

#### Scenario: An empty agent builds a claude command

- **WHEN** `BuildAgentCommand` is called with an empty agent name
- **THEN** the executable is `claude` and the arguments begin with
  `--print --dangerously-skip-permissions`

#### Scenario: A named harness is unchanged

- **WHEN** `BuildAgentCommand` is called with agent `opencode`
- **THEN** the executable and arguments are identical to those produced before
  this change

#### Scenario: Provenance names the resolved default

- **WHEN** an agent phase runs with an empty agent name
- **THEN** the recorded phase payload's `agent` value is `claude`

### Requirement: A missing harness fails with a reason that names it

Before starting an agent attempt, the runner SHALL check that the resolved
harness executable is on `PATH`. When it is not, the attempt SHALL be
recorded `failed` with a reason naming the executable and the
`defaults.agent` project setting, and no process SHALL be started.

#### Scenario: claude is absent from PATH

- **WHEN** an agent phase runs with an empty agent name and `PATH` contains no
  `claude` executable
- **THEN** the phase record is `failed` and its error contains `claude` and
  `defaults.agent`

### Requirement: A phase never adopts another phase's envelope

The runner SHALL remove any existing `.sgt/envelope.json` in the worktree
before each agent attempt starts. An attempt that writes no envelope of its
own SHALL get a synthesized envelope, never one left by an earlier phase or
attempt.

#### Scenario: A review does not inherit the build's envelope

- **WHEN** a `build` attempt writes `.sgt/envelope.json` with summary
  `BUILD-ENVELOPE`, and the following `review` attempt exits 0 without
  writing one
- **THEN** the review phase's recorded envelope summary does not contain
  `BUILD-ENVELOPE`

### Requirement: Process failure decides an attempt before its output does

An attempt that exits non-zero, exceeds its budget, or ends with the
termination notice SHALL be recorded `failed`, whatever its output says. No
verdict, handoff or agent-written envelope SHALL be read from it.

#### Scenario: A PASS before the termination notice is a failure

- **WHEN** a `gauntlet:beta` attempt prints `VERDICT: PASS`, then its final
  line is the termination notice, and it exits 0
- **THEN** the review phase is `failed` with the termination reason

#### Scenario: A PASS with a non-zero exit is a failure

- **WHEN** a `gauntlet:beta` attempt ends with `VERDICT: PASS` and exits 1
- **THEN** the review phase is `failed`

#### Scenario: A judged failure is not summarised as completed

- **WHEN** a beta attempt exits 0 with no verdict and writes no envelope
- **THEN** the recorded envelope summary begins `Phase review failed for`
  and does not contain `completed`

#### Scenario: A failed attempt does not store its own completed envelope

- **WHEN** an attempt writes `.sgt/envelope.json` with summary `DONE` and
  then exits 1
- **THEN** the recorded envelope summary is sgt's failure summary and does
  not contain `DONE`
