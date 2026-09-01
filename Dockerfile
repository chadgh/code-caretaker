# syntax=docker/dockerfile:1

# code-caretaker shells out to claude, gh, git and ssh, so shipping the binary
# alone would leave four prerequisites to install on every box. This image
# carries all of them.

# ---- build ------------------------------------------------------------------
# Built for the host architecture and cross-compiled, so no emulation is needed
# for the Go toolchain itself.
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags "-s -w -X github.com/chadgh/code-caretaker/internal/cli.version=${VERSION}" \
    -o /out/code-caretaker .

# ---- runtime ----------------------------------------------------------------
# node is the base because the claude CLI ships as an npm package.
FROM node:22-bookworm-slim

ARG CLAUDE_CODE_VERSION=latest

# gh comes from GitHub's own apt repository, which carries both amd64 and
# arm64.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates curl git gnupg jq less openssh-client; \
    mkdir -p -m 755 /etc/apt/keyrings; \
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /etc/apt/keyrings/githubcli-archive-keyring.gpg; \
    chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg; \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends gh; \
    apt-get purge -y --auto-remove gnupg; \
    rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
    && npm cache clean --force

# The sessions this tool dispatches run `claude --dangerously-skip-permissions`,
# which refuses to start as root. Take uid 1000 from the base image's `node`
# user so a bind-mounted checkout owned by a typical host user stays writable.
RUN set -eux; \
    userdel -r node; \
    groupadd -g 1000 caretaker; \
    useradd -m -u 1000 -g caretaker -s /bin/bash caretaker; \
    mkdir -p /workspace; \
    chown caretaker:caretaker /workspace

COPY --from=build /out/code-caretaker /usr/local/bin/code-caretaker

# The example config ships with the image so a starting config is one command
# away, with no clone:
#   docker run --rm --entrypoint cat IMAGE \
#     /usr/share/code-caretaker/agent_loop.example.toml > agent_loop.toml
# The license ships because the image redistributes the software.
COPY agent_loop.example.toml /usr/share/code-caretaker/agent_loop.example.toml
COPY LICENSE /usr/share/code-caretaker/LICENSE

# Defaults that make a fresh container able to actually do the work. They go in
# the system config rather than a user's, so they still apply when the image is
# run with `--user` set to some other uid, as CI runners tend to do:
#   * safe.directory, because a bind-mounted repository is usually owned by a
#     different uid than the one inside the container.
#   * a credential helper that defers to gh, so `git push` over HTTPS works
#     from whatever GH_TOKEN/GITHUB_TOKEN gh is already using. This is what
#     `gh auth setup-git` writes; it is inert when gh has no token.
#   * a commit identity, since a session that cannot commit cannot open a PR.
# Override any of them with `git config` in the mounted repository, or with the
# usual GIT_AUTHOR_* / GIT_COMMITTER_* environment variables.
RUN set -eux; \
    git config --system --add safe.directory '*'; \
    git config --system credential.https://github.com.helper ''; \
    git config --system --add credential.https://github.com.helper '!gh auth git-credential'; \
    git config --system user.name 'code-caretaker'; \
    git config --system user.email 'code-caretaker@users.noreply.github.com'

USER caretaker
ENV HOME=/home/caretaker
WORKDIR /workspace

ENTRYPOINT ["code-caretaker"]
CMD ["run"]
