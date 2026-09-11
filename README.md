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
~/snip/<provider>/<account>/<stable-id>-<slug>
```

The remote ID is the identity. Once a clone exists, its directory stays unchanged when a remote
description or title changes. Configure Git's conditional identity includes for these GitHub and
work GitLab paths; `snip` does not copy names or email addresses into repositories.

Failed post-creation clones are recorded below `$XDG_STATE_HOME/snip/pending` (falling back to
`~/.local/state/snip/pending`). Repeating the same command retries that remote instead of creating
a duplicate. Content and credentials are not stored there; the record contains remote identity and
URLs. It is removed after a verified clone.

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

When provider listing is offline after authentication has been resolved, `snip` prints a short
notice and discovers current local clones only. It does not retain a cache of remote-only items.
Missing authentication is a hard error with provider-specific login guidance and never falls back
to another account or provider.

`new` opens an empty temporary file in `$EDITOR` and cancels on editor error or blank content.
`paste` requires a safe basename and reads the clipboard. `file` accepts one readable regular file,
uses its basename, and does not modify it. GitHub defaults to secret/unlisted and GitLab defaults to
private; `--public` is the only public-creation path. Provider, host, account, and visibility are
printed immediately before the remote write.

Generate completion definitions with `snip completion bash`, `snip completion zsh`, or
`snip completion fish`.

## Development

The repository uses only the Go standard library. Tests replace every provider, Git, fzf,
clipboard, editor, and browser process with isolated fakes.

```sh
go test ./...
go vet ./...
go test -race ./...
```

Release builds are described by `.goreleaser.yaml`. Tags may produce checksummed macOS and Linux
archives plus Debian and Arch packages. The included GitHub Actions workflow runs tests only; it
does not publish, tag, or alter repository settings.
