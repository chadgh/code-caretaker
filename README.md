# code-caretaker

An autonomous caretaker for a GitHub repository. It wakes up on an interval,
walks a list of prioritized checks, and hands the first one that finds work to
a Claude Code session — fix the red PR, batch up the Dependabot upgrades,
triage what production logged, implement the issue you labeled. Then it sleeps
and does it again.

You describe the work in one TOML file. Everything else is a container you run.

```sh
docker run --rm \
  -v "$PWD:/workspace" \
  -e REPO=owner/name \
  -e GH_TOKEN -e ANTHROPIC_API_KEY \
  ghcr.io/chadgh/code-caretaker:latest
```

## What a cycle does

1. Returns the working tree to a clean `main` (`git checkout main && git pull`).
2. Confirms Claude usage remains; if not, backs off for `token_sleep_seconds`.
3. Fetches the open pull requests once and shares them with every step.
4. Walks the configured steps **in order**. The first one that finds work
   renders its prompt and dispatches a single Claude Code session. Order is
   priority.
5. Appends a record to a JSONL status feed, then sleeps for
   `check_interval_seconds`.

Exactly one session runs per cycle, so the loop never fans out into parallel
sessions competing over the same checkout.

### The steps

| type                | what it does                                                     |
| ------------------- | ---------------------------------------------------------------- |
| `failing_prs`       | Fix an open PR with red CI checks or a merge conflict.           |
| `dependabot_alerts` | Batch-resolve open Dependabot security alerts in one PR.         |
| `prod_errors`       | Triage recent errors from a production journal over SSH.         |
| `labeled_issues`    | Implement an open issue carrying a given label and open a PR.    |
| `command`           | Escape hatch: run a shell check; if it prints, dispatch.         |

`command` is how you add a new kind of work without writing any Go: it runs a
shell command, and whatever that command prints becomes `{output}` in a prompt
you write.

## Running one step and exiting

The loop is the default, but every step can also be run once from a shell, a
cron entry, or a CI job.

```sh
# Run a step the config declares, then exit. Works even if it is disabled
# there, which is a handy way to keep a step on hand without running it.
code-caretaker step failing_prs

# List what the config declares.
code-caretaker steps

# Define a step entirely on the command line — nothing in the config needed.
code-caretaker step \
  --type command \
  --name stale-branches \
  --param check='git branch -r --merged origin/main | grep -v main' \
  --prompt 'These branches on {repo} look merged:

{output}

Verify each one, then delete it from the remote.'

# See what would be dispatched without dispatching it. A dry run touches
# nothing: no reset, no session, and no Claude usage required.
code-caretaker step labeled_issues --dry-run
```

`--param KEY=VALUE` is repeatable and carries the type-specific settings (the
`check` for a `command` step, the `label` for `labeled_issues`, and so on) —
the same keys the TOML uses.

### Exit codes

| code | meaning                                          |
| ---- | ------------------------------------------------ |
| `0`  | the step ran, or found nothing to do             |
| `1`  | a Claude session ran and failed                  |
| `2`  | configuration or usage error                     |
| `3`  | Claude usage is exhausted — retry later          |

## Configuration

Settings come from `agent_loop.toml` at the repository root.
[`agent_loop.example.toml`](agent_loop.example.toml) is a fully annotated
reference covering every step type and option — start from it:

```sh
cp agent_loop.example.toml agent_loop.toml
```

The image carries a copy, so you can start from it without cloning:

```sh
docker run --rm --entrypoint cat ghcr.io/chadgh/code-caretaker:latest \
  /usr/share/code-caretaker/agent_loop.example.toml > agent_loop.toml
```

```toml
[loop]
repo = "owner/name"
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

Every step accepts four common keys: `enabled` (default true), `name` (the
label used in logs), `prompt` (replace the step's built-in template), and
`max_open_prs` (skip the step while more than N PRs are open — how you stop the
loop creating review work faster than a human can merge it).

A custom `prompt` is a brace-style template validated at startup: `{name}`
fields must be placeholders the step actually provides, and literal braces are
written `{{` and `}}`. A typo fails immediately rather than at 3am.

**Where the config comes from:** the `--config` flag, else `$AGENT_LOOP_CONFIG`,
else `agent_loop.toml` at the repository root, else built-in defaults. For
`[loop]` values the precedence is environment variable > TOML > built-in
default. `repo` is the one setting with no default; the tool refuses to start
until the config or `REPO` supplies it.

| environment variable     | overrides                |
| ------------------------ | ------------------------ |
| `REPO`                   | `repo`                   |
| `CHECK_INTERVAL_SECONDS` | `check_interval_seconds` |
| `TOKEN_SLEEP_SECONDS`    | `token_sleep_seconds`    |
| `CLAUDE_TIMEOUT_SECONDS` | `claude_timeout_seconds` |
| `AGENT_STATUS_FILE`      | `status_file`            |
| `AGENT_LOOP_CONFIG`      | which config file to read |

## Credentials

The tool drives four command-line programs, so it needs whatever they need:

- **`ANTHROPIC_API_KEY`** — for `claude`. To use a Claude subscription instead
  of an API key, mount an authenticated config directory into the container:
  `-v "$HOME/.claude:/home/caretaker/.claude"`.
- **`GH_TOKEN`** (or `GITHUB_TOKEN`) — for `gh`. It needs `repo` scope, plus
  `security_events` for the `dependabot_alerts` step.
- **git push access.** The image configures git to take HTTPS credentials from
  `gh`, so an HTTPS remote works from `GH_TOKEN` alone. For an SSH remote,
  mount a key: `-v "$HOME/.ssh:/home/caretaker/.ssh:ro"`.
- **An SSH key** for the host the `prod_errors` step reads, if you enable it.

The image also sets a default commit identity (`code-caretaker
<code-caretaker@users.noreply.github.com>`). Override it with `git config` in
your checkout or the usual `GIT_AUTHOR_*` variables.

## Deploying

### A long-running container

The image bundles `claude`, `gh`, `git` and `ssh`, so a host only needs Docker.
Give it a checkout to work in and let it run:

```sh
git clone git@github.com:owner/name.git /srv/name
sudo chown -R 1000:1000 /srv/name   # the container runs as uid 1000

docker run -d --name caretaker --restart unless-stopped \
  -v /srv/name:/workspace \
  -e REPO=owner/name \
  -e GH_TOKEN -e ANTHROPIC_API_KEY \
  ghcr.io/chadgh/code-caretaker:latest
```

Or with Compose:

```yaml
services:
  caretaker:
    image: ghcr.io/chadgh/code-caretaker:latest
    restart: unless-stopped
    volumes:
      - /srv/name:/workspace
    environment:
      REPO: owner/name
      GH_TOKEN: ${GH_TOKEN}
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
```

The mounted directory must be a real clone with a remote — the loop pulls at
the start of every cycle and the sessions push branches from it.

### GitHub Actions

Run a single step on a schedule instead of keeping a host awake. Pass
`--no-reset`, because the checkout is already on the ref you want, and
`--user`, so the container writes as the runner:

```yaml
name: Caretaker
on:
  schedule:
    - cron: "*/30 * * * *"
  workflow_dispatch:

jobs:
  caretaker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Fix failing pull requests
        run: |
          docker run --rm \
            --user "$(id -u):$(id -g)" -e HOME=/tmp \
            -v "$PWD:/workspace" \
            -e REPO -e GH_TOKEN -e ANTHROPIC_API_KEY \
            ghcr.io/chadgh/code-caretaker:latest \
            step failing_prs --no-reset
        env:
          REPO: ${{ github.repository }}
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

Exit code `3` means Claude usage ran out; treat it as "try again next
schedule", not as a failure.

One caveat on `secrets.GITHUB_TOKEN`: pushes made with it do not trigger other
workflows, so CI will not run on a pull request the agent opens. Use a personal
access token or a GitHub App token if you need that.

### Without Docker

```sh
go install github.com/chadgh/code-caretaker@latest
```

Then put `claude`, `gh`, `git` and `ssh` on `PATH` yourself and make sure each
is authenticated.

### Image tags

Published to `ghcr.io/chadgh/code-caretaker` on every commit to `main`:

| tag         | what it points at                      |
| ----------- | -------------------------------------- |
| `latest`    | the newest commit on `main`            |
| `sha-<sha>` | one specific commit                    |
| `v1.2.3`, `1.2`, `1` | a released tag                |

Pin `sha-<sha>` or a version tag if you would rather upgrade deliberately.
Images are built for `linux/amd64` and `linux/arm64`.

## Watching it work

Every cycle appends a JSON record to `status_file`:

```sh
tail -f .agent-status/events.jsonl
```

Because every cycle writes at least one line, the newest record's `ts` doubles
as a heartbeat — if it goes stale, the loop has stopped. Records carry
`notify: true` when they are worth interrupting a human for: startup, usage
exhaustion, a failed session, production errors.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the layout, the test suite, and the
git hooks.

## License

[MIT](LICENSE).

That covers this tool. The programs it drives are their own: running it means
bringing your own Claude access, under Anthropic's terms, along with the GitHub
credentials the steps need.
