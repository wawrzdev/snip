# snip

`snip` is a small CLI for personal GitHub gists and account-level GitLab snippets. It discovers
remote items, keeps ordinary Git clones under a predictable local tree, and lets Git remain the
editing and synchronization interface.

The first release deliberately has no `status`, `sync`, `push`, migration, or visibility-change
command. Run normal Git commands inside a clone after creation.

## Install

Build with Go 1.24 or newer:

```sh
go install github.com/wawrzdev/snip/cmd/snip@latest
```

Runtime tools depend on the operation:

- `git` for cloning and `--update`
- `gh` for GitHub authentication and gists
- `glab` for GitLab authentication and snippets
- `fzf` for interactive selection
- `$EDITOR` for `snip new`
- `pbpaste`, `wl-paste`, or `xclip` for `snip paste`
- `$BROWSER`, `open`, or `xdg-open` for `snip web`

The Debian and Arch packages depend on Git, fzf, and the default GitHub provider CLI. That CLI is
packaged as `gh` on Debian and `github-cli` on Arch. GitLab support remains optional; install `glab`
separately when enabling the GitLab provider.

Credentials remain owned by `gh` and `glab`. Sign in with the exact host you configured:

```sh
gh auth login --hostname github.com
glab auth login --hostname gitlab.example.com
```

## Configuration

Configuration is read from `$XDG_CONFIG_HOME/snip/config.toml`, falling back to
`~/.config/snip/config.toml`. With no file, personal GitHub is enabled and is the default.

```toml
default_provider = "gitlab"

[github]
enabled = true
host = "github.com"

[gitlab]
enabled = true
host = "gitlab.example.com"
```

Setting a provider `host` enables it unless `enabled = false` is explicit. The GitLab host must be
the account-level host; `snip` never derives a project from the current directory. Use `--github`
or `--gitlab` to constrain discovery or creation. Creation uses `default_provider` when neither is
given and fails if that provider is disabled. Account names always come from the active provider
CLI session.

Clone paths have this form:

```text
~/snip/<host>/<account>/<readable-name>
```

The readable name comes from description/title, then the first filename stem, then
`snippet-<id>`. An ID suffix is added only when that name collides. Host and account components are
sanitized. The remote ID is recorded under `.git/snip.json` as canonical identity, and the clone's
`origin` must match that provider, host, and ID before `snip` returns or updates it. Once a clone
exists, its directory stays unchanged when a remote description or title changes. Configure Git's
conditional identity includes for these GitHub and work GitLab paths; `snip` does not copy names or
email addresses into repositories.

Creation intents are recorded below `$XDG_STATE_HOME/snip/pending` (falling back to
`~/.local/state/snip/pending`) before provider mutation. The mode-0600 record includes the captured
content so an editor-based retry does not reopen the editor. It moves atomically from `prepared` to
`creating` to `created`. A `created` intent retries only its known remote. An interrupted or
unrecordable provider result remains `creating` and blocks another creation with a recovery path;
inspect the provider and that intent before manually removing or repairing it. The record is
removed after a verified clone.

## Commands

```text
snip [--github|--gitlab] [query]
snip list [--github|--gitlab]
snip get [--github|--gitlab] [--update] <name|url|id>
snip get [--github|--gitlab] [--update] --all
snip new [--github|--gitlab] <filename> [--description text] [--public]
snip paste [--github|--gitlab] <filename> [--description text] [--public]
snip file [--github|--gitlab] <path> [--description text] [--public]
snip web [--github|--gitlab] [query]
snip completion <bash|zsh|fish>
```

Bare `snip` searches every enabled provider online and opens a single-target fzf picker. A query
that has one partial match resolves directly; ambiguous queries open the same picker prefilled.
Escape cancels without cloning or opening anything. `snip get` is stricter: one exact ID,
title/description, filename, filename stem, explicit URL, or unambiguous provider ID resolves
directly; otherwise it opens a prefiltered picker. Ambiguity fails when no TTY is available.

`snip list` never invokes fzf. Its stable tab-separated columns are provider, host, account,
remote ID, cloned (`yes` or `no`), current name, comma-separated filenames, and provider URL.

`snip get --all` acts on one selected or default source. Missing clones are cloned. Existing clones
are left alone unless `--update` is present, which runs a fast-forward-only pull and preserves local
changes on failure. A successful selection or get prints only the verified clone path on stdout so
a shell wrapper can change directory:

```zsh
snip() {
  local action="" arg
  for arg in "$@"; do
    case "$arg" in
      --github|--gitlab) ;;
      *) action="$arg"; break ;;
    esac
  done
  case "$action" in
    list|new|paste|file|web|completion|version|help|-h|--help|--version)
      command snip "$@"
      ;;
    *)
      local path
      path=$(command snip "$@") || return
      [[ -d "$path/.git" ]] || { print -u2 "snip: invalid clone path: $path"; return 1; }
      builtin cd -- "$path"
      ;;
  esac
}
```

When authentication, account lookup, or provider listing fails because the network is unavailable,
`snip` prints a short notice and discovers metadata-backed local clones without requiring an online
account lookup. Explicit IDs can resolve those verified clones offline. It does not retain a cache
of remote-only items. A distinguishable missing-authentication response is a hard error with
provider-specific login guidance and never falls back to another account or provider. Explicit
GitLab URLs must use the configured GitLab host.

`new` opens an empty temporary file in `$EDITOR` and cancels on editor error or blank content.
`paste` requires a safe basename and reads the clipboard. `file` accepts one readable regular file,
uses its basename, and does not modify it. GitHub defaults to secret/unlisted and GitLab defaults to
private; `--public` is the only public-creation path. Provider, host, account, and visibility are
printed immediately before the remote write.

Generate completion definitions with `snip completion bash`, `snip completion zsh`, or
`snip completion fish`.

## Development

The production binary uses only the Go standard library. Tests use `github.com/creack/pty` for
cross-platform terminal coverage and replace every provider, Git, fzf, clipboard, editor, and
browser process with isolated fakes.

```sh
go fmt ./...
go test ./...
go vet ./...
go test -race ./...
./scripts/generate-completions.sh
git diff --exit-code -- completions
```

Before committing completion changes, run the generator and commit its output. The Go suite compares
all checked-in completions byte-for-byte with runtime output. CI runs that suite alongside formatting,
vet, and race tests on Ubuntu, macOS, and Arch Linux. It also validates `.goreleaser.yaml` with
GoReleaser v2.

Release tags must be stable `vMAJOR.MINOR.PATCH` versions on commits already merged to the
current default branch. GoReleaser builds four macOS/Linux archives and four Debian/Arch packages,
uploading them plus `checksums.txt` to a **draft**. Before publishing, the workflow checks the exact
nine uploaded names, sizes, and SHA-256 digests against local artifacts and the checksum file.
Release runs are serialized across tags and reject versions no newer than the current latest.
Only then is the draft published and made immutable by the repository's enabled immutability setting.
`release.mode` is a release-notes policy, not an immutability control; see
[GoReleaser release behavior](https://goreleaser.com/customization/publish/scm/) and
[GitHub immutable-release publishing](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases).

A separate, checkout-free `dispatch` job verifies final immutable metadata and sends
`snip-release-published` to `wawrzdev/packages`. Configure a `package-dispatch` environment in this
source repository, restricted to `v*` tags, with variable `PACKAGES_APP_CLIENT_ID` and secret
`PACKAGES_APP_PRIVATE_KEY`. Use the existing GitHub App installed **only on `wawrzdev/packages`**;
it does not need installation access to this source repository. The job requests a short-lived
installation token restricted to `packages` with Contents write permission only. Source release
lookup/publication uses the normal workflow token; the App key is never present during checkout,
builds, tests, or draft publication. No PAT is required.

If dispatch fails after publication, fix the environment and rerun only the failed dispatch job.
Do not rebuild or replace immutable release assets. The packages repository's scheduled or manual
reconciliation can also discover a published release without a successful dispatch. If draft
validation fails, leave it unpublished, correct the cause, and inspect/remove the failed draft
before retrying. Feed construction, PR auto-merge, signing, and deployment belong to `packages`.
