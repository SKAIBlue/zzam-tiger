package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type MissingCLIError struct {
	Provider string
	Command  string
}

func (e *MissingCLIError) Error() string {
	return fmt.Sprintf("%s CLI (%s) is not installed", e.Provider, e.Command)
}

func (e *MissingCLIError) InstallGuide() string {
	if e.Command == "gh" {
		return "Install GitHub CLI, then authenticate:\n" +
			"  macOS:  brew install gh\n" +
			"  Linux:  https://github.com/cli/cli/blob/trunk/docs/install_linux.md\n" +
			"  Auth:   gh auth login"
	}
	return "Install GitLab CLI, then authenticate:\n" +
		"  macOS:  brew install glab\n" +
		"  Linux:  https://gitlab.com/gitlab-org/cli#installation\n" +
		"  Auth:   glab auth login"
}

func IsMissingCLI(err error) (*MissingCLIError, bool) {
	var target *MissingCLIError
	ok := errors.As(err, &target)
	return target, ok
}

func Detect(requested, repo string, runner Runner) (Provider, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" && requested != "auto" && requested != "github" && requested != "gitlab" {
		return nil, fmt.Errorf("unsupported provider %q (use auto, github, or gitlab)", requested)
	}

	host := ""
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	remote, remoteErr := runner.Run(ctx, "git", "remote", "get-url", "origin")
	if remoteErr == nil {
		var remoteRepo string
		var ok bool
		host, remoteRepo, ok = ParseRepositoryURL(string(remote))
		if !ok {
			host = ""
		} else if repo == "" {
			repo = remoteRepo
		}
	}

	if requested == "auto" || requested == "" {
		switch {
		case strings.EqualFold(host, "github.com"):
			requested = "github"
		case strings.EqualFold(host, "gitlab.com"):
			requested = "gitlab"
		case host != "":
			requested = detectSelfHostedProvider(ctx, host, runner)
			if requested == "" {
				return nil, fmt.Errorf("could not detect whether %q is GitHub Enterprise or self-managed GitLab; pass --provider github or --provider gitlab", host)
			}
		case remoteErr != nil && repo != "":
			return nil, fmt.Errorf("no origin remote to identify the provider for %q; pass --provider github or --provider gitlab", repo)
		case remoteErr != nil:
			return nil, fmt.Errorf("this directory has no readable origin remote; run zt inside a GitHub/GitLab repository")
		default:
			return nil, fmt.Errorf("could not detect provider from origin; pass --provider github or --provider gitlab")
		}
	}

	command := map[string]string{"github": "gh", "gitlab": "glab"}[requested]
	if err := runner.LookPath(command); err != nil {
		providerName := map[string]string{"github": "GitHub", "gitlab": "GitLab"}[requested]
		return nil, &MissingCLIError{Provider: providerName, Command: command}
	}

	switch requested {
	case "github":
		value, err := NewGitHub(runner, repo, host)
		if err != nil {
			return nil, err
		}
		return CachedProvider(value), nil
	case "gitlab":
		value, err := NewGitLab(runner, repo, host)
		if err != nil {
			return nil, err
		}
		return CachedProvider(value), nil
	default:
		return nil, fmt.Errorf("could not detect provider")
	}
}

func detectSelfHostedProvider(ctx context.Context, host string, runner Runner) string {
	type candidate struct {
		provider string
		command  string
		args     []string
	}
	candidates := []candidate{
		{provider: "github", command: "gh", args: []string{"auth", "status", "--active", "--hostname", host}},
		{provider: "gitlab", command: "glab", args: []string{"auth", "status", "--hostname", host}},
	}

	detected := ""
	for _, candidate := range candidates {
		if runner.LookPath(candidate.command) != nil {
			continue
		}
		if _, err := runner.Run(ctx, candidate.command, candidate.args...); err == nil {
			if detected != "" {
				return ""
			}
			detected = candidate.provider
		}
	}
	return detected
}
