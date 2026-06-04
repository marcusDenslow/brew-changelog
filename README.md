# brew-changelog

Show GitHub release notes for the version range between your installed and the
latest available Homebrew formulae.

## Why

`brew outdated` tells you *what* is behind, not *what changed*. This walks the
gap by querying GitHub Releases for every formula whose homepage points at a
GitHub repo, then prints the release notes for tags strictly above your
installed version and at-or-below `versions.stable`.

## Install

```bash
chmod +x brew-changelog
ln -s "$PWD/brew-changelog" /opt/homebrew/bin/brew-changelog
# Now usable as both:
brew-changelog
brew changelog          # brew picks up any executable named brew-<name>
```

## Deps

- `brew` (Homebrew)
- `gh` (GitHub CLI) — must be `gh auth login`'d
- `jq`

## Usage

```bash
brew changelog                  # all outdated formulae
brew changelog ghostty fzf gh   # specific formulae (skip the outdated check)
```

## Known limitations (v1)

- Formulae only — casks ignored
- GitHub only — GitLab, Codeberg, project sites print a "manual check" note
- Version comparison uses `sort -V`; oddities like `1.0.0-rc1` vs `1.0.0` may
  sort unexpectedly. Most stable releases are fine.
- No caching — every run hits the GitHub API.
