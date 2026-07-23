package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/tui"
	"github.com/SKAIBlue/zzam-tiger/internal/updater"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

var version = "dev"

func main() {
	providerName := flag.String("provider", "auto", "provider: auto, github, or gitlab")
	repo := flag.String("repo", "", "repository (owner/name or group/project); defaults to the current git repository")
	refresh := flag.Duration("refresh", 5*time.Second, "automatic refresh interval (0 disables)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	backend, err := provider.Detect(*providerName, *repo, provider.ExecRunner{})
	if err != nil {
		if missing, ok := provider.IsMissingCLI(err); ok {
			fmt.Fprintf(os.Stderr, "zt: %v\n\n%s\n", err, missing.InstallGuide())
		} else {
			fmt.Fprintf(os.Stderr, "zt: %v\n", err)
		}
		os.Exit(1)
	}

	workspace := worktree.New(".", provider.ExecRunner{})
	model := tui.NewWithWorktree(backend, *refresh, workspace).WithUpdates(version, updater.CheckLatest, updater.InstallCommand)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "zt: %v\n", err)
		os.Exit(1)
	}
}
