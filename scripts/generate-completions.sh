#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

mkdir -p completions
go run ./cmd/snip completion bash >completions/snip.bash
go run ./cmd/snip completion zsh >completions/_snip
go run ./cmd/snip completion fish >completions/snip.fish
