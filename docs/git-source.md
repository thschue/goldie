# Serve your Source from git

A git repository is the usual home of a golden path: changes are reviewed, and the history is yours.
This guide takes a repository to a running `goldie server` over it. The settings are in the
[configuration reference](reference/configuration.md); this page shows how they fit together. It
covers one git Source.

## The repository layout

The server reads the repository's root as the Source, so `global/articles/` sits at the top:

```text
global/articles/deploy.md
```

The files, the frontmatter and the rest of the layout are in [Writing content](content.md).

## Point the server at it

`GOLDIE_GIT_URL` is the repository, in place of `GOLDIE_CONTENT_DIR` (setting both stops the server).
`GOLDIE_GIT_REF` is the branch, tag or full ref (`refs/...`) to serve; unset, the remote's HEAD.

```sh
# https
export GOLDIE_GIT_URL=https://git.example.com/platform/golden-paths.git
export GOLDIE_GIT_REF=main

# ssh
export GOLDIE_GIT_URL=ssh://git@git.example.com/platform/golden-paths.git
# or, scp-like (the user is required)
export GOLDIE_GIT_URL=git@git.example.com:platform/golden-paths.git
```

Besides the repository, the server needs an API token, as with any Source (`GOLDIE_API_TOKEN` or
`GOLDIE_API_TOKENS`):

```sh
export GOLDIE_API_TOKEN="$(openssl rand -hex 32)"
goldie server
```

Keep credentials out of the URL. The accepted URL forms are listed under `GOLDIE_GIT_URL` in the
reference.

## A private repository

Secrets are read from files (`NAME_FILE`), never from the command line or the repository.

For https, a token, sent as the basic-auth password:

```sh
export GOLDIE_GIT_TOKEN_FILE=/run/secrets/git-token
```

For ssh, an unencrypted PEM private key and the `known_hosts` lines the host key is checked
against. There is no way to skip that check:

```sh
export GOLDIE_GIT_SSH_KEY_FILE=/run/secrets/git-ssh-key
export GOLDIE_GIT_SSH_KNOWN_HOSTS_FILE=/run/secrets/git-known-hosts
```

An https host behind an internal CA is trusted with `GOLDIE_GIT_CA_BUNDLE`.

## How changes arrive

The server polls the repository every `GOLDIE_POLL_INTERVAL` (default `1m`, at least `10s`):

```sh
export GOLDIE_POLL_INTERVAL=30s
```

- **Before the first clone.** The server starts without waiting for it. `/readyz` on the admin
  listener (`GOLDIE_ADMIN_ADDR`) fails `source:git`, and `/api/v1` answers 503 `not_ready`, until
  the first load succeeds.
- **When a poll fails.** The server keeps the last good content and logs a warning. The next poll
  tries again.

## Check the repository in CI

`goldie validate <dir>` runs the load the server runs, so content that passes it loads on the server.
Run it on the checkout in the pipeline that guards your branch, and fail the job on a non-zero exit:
see [Checking content in CI](content-ci.md).
