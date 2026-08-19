#!/usr/bin/env bash
# Fetch a reference project at a pinned commit for an architecture gate.
#
# Architecture gates cite official repositories at a named commit. Reading a
# repository through the GitHub API is enough to establish that a path exists;
# it is not enough to grep a subsystem. This fetches a shallow, read-only,
# pinned working copy so a gate can be researched properly and re-derived by
# anyone reviewing it.
#
# The checkout is deliberately disposable and gitignored:
#
#   * Documentation rule 7 requires each later gate to RE-VERIFY sources at
#     their then-current state. A long-lived clone invites citing a stale
#     commit, so this pins a SHA explicitly and never tracks a branch.
#   * Nothing fetched here may be copied. Every gate states that it does not
#     authorize copying reference-project types, schemas, or runtime. This is
#     for reading and citing only.
#
# Usage:
#   scripts/fetch-reference.sh <owner/repo> <commit-sha> [name]
#
# Example:
#   scripts/fetch-reference.sh earendil-works/pi 59a71b2 pi
#   rg 'createSessionBackendConformance' .reference/pi
#
# List what is currently fetched:
#   scripts/fetch-reference.sh --list

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cache="$root/.reference"

if [[ "${1:-}" == "--list" ]]; then
  if [[ ! -d "$cache" ]]; then
    echo "no reference checkouts in $cache"
    exit 0
  fi
  for dir in "$cache"/*/; do
    [[ -d "$dir/.git" ]] || continue
    printf '%-24s %s  %s\n' \
      "$(basename "$dir")" \
      "$(git -C "$dir" rev-parse --short HEAD)" \
      "$(git -C "$dir" remote get-url origin 2>/dev/null || echo '?')"
  done
  exit 0
fi

if [[ $# -lt 2 ]]; then
  sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 2
fi

repo="$1"
sha="$2"
name="${3:-$(basename "$repo")}"
dest="$cache/$name"

mkdir -p "$cache"

if [[ -d "$dest/.git" ]]; then
  # A previous run may have failed before any commit existed, so an empty
  # repository is a normal state here, not an error.
  current="$(git -C "$dest" rev-parse HEAD 2>/dev/null || true)"
  if [[ -n "$current" && "$current" == "$sha"* ]]; then
    echo "$name already at $sha"
    exit 0
  fi
  if [[ -n "$current" ]]; then
    echo "$name is at ${current:0:7}, refetching at $sha"
  fi
  git -C "$dest" remote set-url origin "https://github.com/$repo.git"
else
  git init -q "$dest"
  git -C "$dest" remote add origin "https://github.com/$repo.git"
fi

git -C "$dest" fetch -q --depth 1 origin "$sha"
git -C "$dest" checkout -q FETCH_HEAD

resolved="$(git -C "$dest" rev-parse HEAD)"
echo "$name  $repo  $resolved"
echo
echo "Cite this in a gate as: $repo \`${resolved:0:7}\`, observed $(date -u +%Y-%m-%d)"
