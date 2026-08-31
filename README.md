# code-caretaker

An autonomous agent loop, written in Go. It wakes up on an interval, walks a
list of prioritized steps, and dispatches a Claude Code session for the first
step that finds work — then sleeps and repeats.

This is a Go port of the Python `scripts/agent_loop` package from
[class-cash](https://github.com/chadgh/class-cash). The behavior and
configuration format are intentionally identical.

## How it works

Each cycle:

1. Resets the working tree to a clean `main` (`git checkout main && git pull`).
2. Checks that Claude usage remains; if not, sleeps for `token_sleep_seconds`.
3. Fetches the open PRs once and shares them with every step.
4. Walks the configured steps in order. The **first** step that finds work
   renders its prompt and dispatches a `claude` session. Order is priority.
5. Emits a record to the append-only JSONL status feed and sleeps.

### Steps

| type                | what it does                                                        |
| ------------------- | ------------------------------------------------------------------- |
| `failing_prs`       | Fix an open PR with red CI checks or a merge conflict.              |
| `dependabot_alerts` | Batch-resolve open Dependabot security alerts in one PR.            |
| `prod_errors`       | Triage recent errors from a production journal over SSH.            |
| `labeled_issues`    | Implement an open issue carrying a given label and open a PR.       |
| `command`           | Generic escape hatch: run a shell check; if it prints, dispatch.    |

## Configuration

Configuration is read from `agent_loop.toml`. Resolution order for the config
file: the `--config` flag, else `$AGENT_LOOP_CONFIG`, else `agent_loop.toml` at
the repo root, else built-in defaults. For `[loop]` values, precedence is
env var > TOML > built-in default.

```toml
[loop]
repo = "chadgh/class-cash"
check_interval_seconds = 300
token_sleep_seconds = 3600
claude_timeout_seconds = 1800
status_file = ".agent-status/events.jsonl"

[[step]]
type = "failing_prs"

[[step]]
type = "labeled_issues"
label = "for-agent"
max_open_prs = 3
```

Every step accepts these common keys: `enabled` (default true), `name` (label
for logs), `prompt` (override the built-in template), and `max_open_prs` (skip
the step when more than N PRs are open). A custom `prompt` is a Python
`str.format`-style template validated at startup — `{name}` fields must be one
of the step's known placeholders, and literal braces are escaped as `{{`/`}}`.

`[loop]` values can be overridden by these env vars: `REPO`,
`CHECK_INTERVAL_SECONDS`, `TOKEN_SLEEP_SECONDS`, `CLAUDE_TIMEOUT_SECONDS`,
`AGENT_STATUS_FILE`, and the config path via `AGENT_LOOP_CONFIG`.

## Running

```sh
go build -o agentloop .
./agentloop                       # uses ./agent_loop.toml or built-in defaults
./agentloop --config other.toml   # explicit config
./agentloop --repo-root /path/to/repo
```

The loop shells out to `claude`, `gh`, `git`, and (for `prod_errors`) `ssh`, so
those must be on `PATH` and authenticated.

## Layout

```
main.go                 entrypoint: flags, startup, the sleep/cycle loop
internal/config         TOML + env config loading and validation
internal/core           shared types, the Step interface, prompt rendering
internal/steps          the concrete steps and the type registry
internal/gh             shared GitHub queries (open PRs, mergeability)
internal/git            git helpers
internal/session        dispatching claude sessions and reading outcomes
internal/status         logging and the append-only JSONL status feed
```

## Development

```sh
go test ./...
go vet ./...
```
