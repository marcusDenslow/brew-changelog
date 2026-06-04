// Command brew-changelog prints GitHub release notes for outdated Homebrew formulae.
//
// Usage:
//
//	brew-changelog                  # all outdated formulae, flat output
//	brew-changelog fzf gh mise      # specific formulae, flat output
//
// Phase 1b: cobra + fang shell for styled --help, lipgloss + glamour for output.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/marcusDenslow/brew-changelog/internal/brew"
	"github.com/marcusDenslow/brew-changelog/internal/release"
	"github.com/marcusDenslow/brew-changelog/internal/render"
	"github.com/marcusDenslow/brew-changelog/internal/source"
)

// Version is set by ldflags at build time; default for local builds.
var Version = "dev"

// noMap, when true, bypasses the embedded sources.json lookup and forces
// the legacy regex URL discovery path. Default behavior (noMap=false) is
// to consult the db first and fall back to regex only on miss. Bound by
// the --no-map flag below; kept package-level so processPkg can read it
// without changing its signature.
var noMap bool

// Catppuccin Mocha — match the render package's palette.
var (
	cpPeach = lipgloss.Color("#fab387") // brew accent
	cpBlue  = lipgloss.Color("#42A0FA")
)

var logoBlock = lipgloss.NewStyle().
	Bold(true).
	Foreground(cpPeach).
	Border(lipgloss.RoundedBorder()).
	BorderForeground(cpPeach).
	Padding(0, 2).
	Render("brew · changelog")

var rootCmd = &cobra.Command{
	Use:   "brew-changelog [formula...]",
	Short: "Show GitHub release notes for outdated Homebrew formulae.",
	Long: lipgloss.JoinVertical(
		lipgloss.Left,
		logoBlock,
		"",
		"Show GitHub release notes for the version gap between your installed",
		"and the latest available Homebrew formulae.",
		"",
		lipgloss.NewStyle().Faint(true).Italic(true).
			Render("Source: https://github.com/marcusDenslow/brew-changelog"),
	),
	Example: `  # Show changes for every outdated formula
  brew changelog

  # Show changes for specific formulae
  brew changelog fzf gh mise

  # Pipe to a pager (ANSI auto-strips when stdout is not a TTY)
  brew changelog | less -R`,
	Args:    cobra.ArbitraryArgs,
	Version: Version,
	RunE:    runChangelog,
}

func main() {
	rootCmd.Flags().BoolVar(&noMap, "no-map", false,
		"skip embedded sources.json lookup; force legacy regex URL discovery (debug / benchmark)")

	themeFunc := fang.WithColorSchemeFunc(func(ld lipgloss.LightDarkFunc) fang.ColorScheme {
		def := fang.DefaultColorScheme(ld)
		def.Title = cpPeach
		def.Command = cpPeach
		def.Program = cpPeach
		def.Flag = lipgloss.Color("#42A0FA")
		return def
	})
	if err := fang.Execute(
		context.Background(),
		rootCmd,
		themeFunc,
		fang.WithVersion(Version),
		fang.WithoutCompletions(),
		fang.WithoutManpage(),
	); err != nil {
		os.Exit(1)
	}
}

func runChangelog(_ *cobra.Command, args []string) error {
	if err := preflight(); err != nil {
		return err
	}
	if len(args) == 0 {
		outdated, err := brew.Outdated()
		if err != nil {
			return err
		}
		if len(outdated) == 0 {
			fmt.Println("all formulae up to date")
			return nil
		}
		args = outdated
	}
	for _, name := range args {
		processPkg(name)
	}
	return nil
}

func preflight() error {
	for _, c := range []string{"brew", "gh"} {
		if _, err := exec.LookPath(c); err != nil {
			return fmt.Errorf("%q not in PATH", c)
		}
	}
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		return fmt.Errorf("gh not authenticated. run: gh auth login")
	}
	return nil
}

func processPkg(name string) {
	info, err := brew.Info(name)
	if err != nil {
		render.Skip(name, "not a formula")
		return
	}
	if info.Installed == "" || info.LatestStable == "" {
		render.Skip(name, "no version info")
		return
	}
	installed := brew.NormalizeVersion(info.Installed)
	latest := brew.NormalizeVersion(info.LatestStable)
	if installed == latest {
		return
	}

	render.Header(name, installed, latest)

	owner, repo, ok := source.Resolve(name, info, !noMap)
	if !ok {
		render.NonGitHub(info.Homepage)
		return
	}

	releases, err := release.Fetch(owner, repo)
	if err != nil {
		render.FetchError(owner, repo)
		return
	}
	if len(releases) == 0 {
		render.NoReleases(owner, repo)
		return
	}
	matched := release.Filter(releases, installed, latest)
	if len(matched) == 0 {
		render.NoMatching(owner, repo, installed, latest)
		return
	}
	for _, r := range matched {
		render.FlatRelease(r, owner, repo)
	}
}
