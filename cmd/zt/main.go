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
	refresh := flag.Duration("refresh", 5*time.Second, "remote provider refresh interval (0 disables; local tabs use filesystem events)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	runner := provider.ExecRunner{}
	workspace, _ := worktree.Open(".", runner)
	var model tui.Model
	if workspace == nil {
		// Outside a Git repository, zt is a filesystem browser. Remote tabs are
		// intentionally hidden because there is no local repository context.
		model = tui.NewFilesOnly(worktree.NewFilesystem("."))
	} else {
		backend, remoteErr := provider.Detect(*providerName, *repo, runner)
		model = tui.NewWithWorktree(backend, *refresh, workspace)
		if remoteErr != nil {
			if missing, ok := provider.IsMissingCLI(remoteErr); ok {
				remoteErr = fmt.Errorf("%w; %s", remoteErr, missing.InstallGuide())
			}
			model = model.WithRemoteUnavailable(remoteErr)
		}
	}
	model = model.WithUpdates(version, updater.CheckLatest, updater.InstallCommand)
	defer model.Close()
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "zt: %v\n", err)
		os.Exit(1)
	}
}
