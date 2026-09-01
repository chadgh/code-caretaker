# Contributing

This is the guide to working *on* code-caretaker. For running it, see the
[README](README.md).

## Getting set up

You need Go (the version in [`go.mod`](go.mod)) and, for the container work,
Docker. Then:

```sh
make hooks    # install the pre-commit hook (see below)
make verify   # gofmt check, go vet, go test, go build
```

`make help` lists every target.

| target         | what it does                                          |
| -------------- | ----------------------------------------------------- |
| `verify`       | everything CI checks: format, vet, test, build        |
| `test`         | the test suite                                        |
| `fmt`          | `gofmt -w -s` over the tree                           |
| `build`        | `./code-caretaker`, stamped with the git version      |
| `run`          | the loop against this repo, using `agent_loop.toml`   |
| `image`        | the container image, for this machine's architecture  |
| `image-verify` | build the image and confirm its bundled tools run     |

## Layout

```
main.go                 entrypoint; hands off to internal/cli
internal/cli            argument parsing, the subcommands, exit codes
internal/config         TOML + env config loading and validation
internal/core           shared types, the Step interface, prompt rendering
internal/steps          the concrete steps and the type registry
internal/gh             shared GitHub queries (open PRs, mergeability)
internal/git            git helpers
internal/session        dispatching claude sessions and reading outcomes
internal/status         logging and the append-only JSONL status feed
```

Two ideas carry most of the design:

**One pass, no waiting.** `loop.RunOnce` makes a single pass over the steps and
returns a `Result` describing what happened. It never sleeps. `loop.Run` adds
the sleeping to build the long-running loop; the `step` command reads the same
`Result` and turns it into an exit code. Anything that decides *how long to
wait* belongs to the caller, not to the pass.

**Steps are a template method.** `core.Check` applies the guards every step
shares (right now, the `max_open_prs` cap) and then calls the step's own
`FindWork`. A step returns a `*core.Finding` — a label and a fully rendered
prompt — or `nil` for "nothing here". A step never dispatches a session itself.

## Adding a step type

1. Add `internal/steps/your_step.go` with a `YourStepType` constant, a struct
   embedding `core.Base`, a `newYourStep(core.StepConfig) (core.Step, error)`
   constructor, and a `FindWork` method.
2. Declare the prompt template as a package constant and pass the placeholder
   names it accepts to `core.NewBase`. Those names are what a user's custom
   `prompt` is validated against at startup, so keep them accurate.
3. Register the type in `stepTypes` in `internal/steps/registry.go`.
4. Read type-specific settings out of `cfg.Params` with the `paramStr` /
   `paramInt` helpers, and return a `*core.ConfigError` for anything missing or
   malformed. Configuration mistakes should fail at startup, never mid-cycle.
5. Document it in `agent_loop.example.toml` — params and placeholders — and add
   a row to the table in the README.

Before writing Go, check whether a `command` step already covers it. That is
what the escape hatch is for; promote it to a real type once it has earned its
keep.

## Testing

External commands are reached through package-level function variables
(`runCommand`, `runOutput`, `runCheck`, `sshRun`) so tests can substitute them.
Follow that pattern rather than shelling out from a new call site — nothing in
the test suite should invoke `claude`, `gh`, `git` or `ssh` for real.

`internal/loop`'s tests replace the whole collaborator set in `setup`, which is
the model for testing anything that spans packages.

## Git hooks

A `pre-commit` hook in `.githooks/` runs `make verify`, so a commit is refused
if the tree is unformatted or fails vet, tests, or the build. A clone does not
install hooks, so enable them once per checkout:

```sh
make hooks             # git config core.hooksPath .githooks
make hooks-uninstall   # back to .git/hooks/
```

The hook checks the working tree rather than the index, and warns when the two
differ. To bypass it for a single commit:

```sh
git commit --no-verify
SKIP_HOOKS=1 git commit
```

## CI and releases

- [`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs `make verify` on
  every push to `main` and every pull request.
- [`.github/workflows/image.yml`](.github/workflows/image.yml) builds the
  image for `linux/amd64` and `linux/arm64`. Pull requests build it to catch a
  broken Dockerfile; pushes publish it to `ghcr.io/<owner>/code-caretaker`,
  tagged `latest` and `sha-<sha>`.

To cut a release, push a `v*` tag. The same workflow adds the semver tags
(`v1.2.3`, `1.2`, `1`), and `git describe` stamps the version that
`code-caretaker version` reports.

Changing the runtime image means changing the [`Dockerfile`](Dockerfile).
`make image-verify` builds it and checks that `git`, `gh`, `ssh` and `claude`
all run inside it — worth doing locally, since a missing tool only shows up as
a failed session at runtime.
