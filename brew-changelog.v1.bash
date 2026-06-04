#!/usr/bin/env bash
# brew-changelog — show GitHub release notes for outdated Homebrew formulae.
#
# Usage:
#   brew-changelog                    # all outdated formulae
#   brew-changelog ghostty fzf gh     # specific formulae (outdated or not)
#
# Deps: brew, gh (authenticated), jq, sort -V (coreutils / BSD sort both fine)

set -euo pipefail

# --- Preflight ---------------------------------------------------------------
for cmd in brew gh jq; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "error: '$cmd' not in PATH" >&2; exit 1; }
done
gh auth status >/dev/null 2>&1 || {
  echo "error: gh not authenticated. run: gh auth login" >&2
  exit 1
}

# --- Style -------------------------------------------------------------------
if [ -t 1 ]; then
  BOLD=$'\e[1m'; DIM=$'\e[2m'; CYAN=$'\e[36m'; GREEN=$'\e[32m'; RESET=$'\e[0m'
else
  BOLD=''; DIM=''; CYAN=''; GREEN=''; RESET=''
fi

# --- Helpers -----------------------------------------------------------------

# Strip leading 'v' and brew's '_N' revision suffix (e.g. "1.2.3_1" -> "1.2.3").
normalize_ver() {
  local v="${1#v}"
  printf '%s' "${v%%_*}"
}

# Return 0 if $1 < $2 under `sort -V` (version sort). Treats equal as false.
ver_lt() {
  [ "$1" = "$2" ] && return 1
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$1" ]
}

# --- Per-package ------------------------------------------------------------
show_pkg() {
  local name="$1" info installed latest homepage head_url stable_url owner repo candidate
  info=$(brew info --json=v2 --formula "$name" 2>/dev/null) || {
    printf '%sskip %s (not a formula)%s\n' "$DIM" "$name" "$RESET"
    return
  }

  installed=$(jq -r '.formulae[0].installed[0].version // ""' <<< "$info")
  latest=$(jq -r '.formulae[0].versions.stable // ""' <<< "$info")
  homepage=$(jq -r '.formulae[0].homepage // ""' <<< "$info")
  head_url=$(jq -r '.formulae[0].urls.head.url // ""' <<< "$info")
  stable_url=$(jq -r '.formulae[0].urls.stable.url // ""' <<< "$info")

  if [ -z "$installed" ] || [ -z "$latest" ]; then
    printf '%sskip %s (no version info)%s\n' "$DIM" "$name" "$RESET"
    return
  fi

  installed=$(normalize_ver "$installed")
  latest=$(normalize_ver "$latest")

  [ "$installed" = "$latest" ] && return  # silent: already up to date

  printf '\n%s── %s %s → %s ──%s\n' "$BOLD$CYAN" "$name" "$installed" "$latest" "$RESET"

  # Try homepage, then HEAD git URL, then stable tarball URL — first GitHub match wins.
  owner=""; repo=""
  for candidate in "$homepage" "$head_url" "$stable_url"; do
    if [[ "$candidate" =~ github\.com/([^/]+)/([^/]+) ]]; then
      owner="${BASH_REMATCH[1]}"
      repo="${BASH_REMATCH[2]%.git}"
      repo="${repo%/}"
      break
    fi
  done

  if [ -z "$owner" ] || [ -z "$repo" ]; then
    printf '  %sno GitHub source found (homepage: %s) — manual check%s\n' "$DIM" "$homepage" "$RESET"
    return
  fi

  local releases
  releases=$(gh api "repos/$owner/$repo/releases?per_page=100" 2>/dev/null) || {
    printf '  %scould not fetch releases for %s/%s%s\n' "$DIM" "$owner" "$repo" "$RESET"
    return
  }

  if [ "$(jq 'length' <<< "$releases")" -eq 0 ]; then
    printf '  %sno releases published at %s/%s%s\n' "$DIM" "$owner" "$repo" "$RESET"
    return
  fi

  local found=0
  # Iterate oldest -> newest (jq `reverse`) for natural reading order.
  # Each release encoded as base64 so we can survive embedded newlines in body.
  while IFS= read -r encoded; do
    [ -z "$encoded" ] && continue
    local rel raw_tag tag relname date body
    rel=$(printf '%s' "$encoded" | base64 -d)
    raw_tag=$(jq -r '.tag_name // ""' <<< "$rel")
    [ -z "$raw_tag" ] && continue
    tag=$(normalize_ver "$raw_tag")

    if ver_lt "$installed" "$tag" && { [ "$tag" = "$latest" ] || ver_lt "$tag" "$latest"; }; then
      found=1
      relname=$(jq -r '.name // ""' <<< "$rel")
      date=$(jq -r '.published_at // ""' <<< "$rel")
      body=$(jq -r '.body // ""' <<< "$rel")
      printf '\n  %s▸ %s%s %s(%s)%s  %s\n' \
        "$GREEN" "$raw_tag" "$RESET" "$DIM" "${date%T*}" "$RESET" "$relname"
      printf '%s\n' "$body" | head -40 | sed 's/^/    /'
      if [ "$(printf '%s\n' "$body" | wc -l)" -gt 40 ]; then
        printf '    %s…(full notes: https://github.com/%s/%s/releases/tag/%s)%s\n' \
          "$DIM" "$owner" "$repo" "$raw_tag" "$RESET"
      fi
    fi
  done < <(jq -r 'reverse | .[] | @base64' <<< "$releases")

  if [ $found -eq 0 ]; then
    printf '  %sno matching tags between %s and %s — browse: https://github.com/%s/%s/releases%s\n' \
      "$DIM" "$installed" "$latest" "$owner" "$repo" "$RESET"
  fi
}

# --- Entry ------------------------------------------------------------------
if [ $# -gt 0 ]; then
  for name in "$@"; do show_pkg "$name"; done
else
  pkgs=$(brew outdated --json=v2 | jq -r '.formulae[].name')
  if [ -z "$pkgs" ]; then
    echo "all formulae up to date"
    exit 0
  fi
  while IFS= read -r name; do
    [ -n "$name" ] && show_pkg "$name"
  done <<< "$pkgs"
fi
