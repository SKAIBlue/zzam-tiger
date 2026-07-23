package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	paths map[string]bool
	run   func(string, ...string) ([]byte, error)
	calls [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.run != nil {
		return f.run(name, args...)
	}
	return nil, nil
}

func (f *fakeRunner) LookPath(name string) error {
	if f.paths[name] {
		return nil
	}
	return errors.New("not found")
}

func TestParseRepositoryURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		host string
		repo string
	}{
		{"github ssh", "git@github.com:owner/repo.git", "github.com", "owner/repo"},
		{"gitlab https nested", "https://gitlab.com/group/sub/project.git", "gitlab.com", "group/sub/project"},
		{"self hosted ssh url", "ssh://git@gitlab.example.test:2222/team/repo.git", "gitlab.example.test", "team/repo"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, repo, ok := ParseRepositoryURL(test.raw)
			if !ok || host != test.host || repo != test.repo {
				t.Fatalf("ParseRepositoryURL(%q) = %q, %q, %t", test.raw, host, repo, ok)
			}
		})
	}
}

func TestDetectUsesOriginBeforeInstalledAlternative(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"glab": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("git@github.com:owner/repo.git\n"), nil
			}
			return nil, nil
		},
	}

	_, err := Detect("auto", "", runner)
	missing, ok := IsMissingCLI(err)
	if !ok {
		t.Fatalf("expected MissingCLIError, got %v", err)
	}
	if missing.Command != "gh" || !strings.Contains(missing.InstallGuide(), "gh auth login") {
		t.Fatalf("unexpected missing CLI guide: %#v, %q", missing, missing.InstallGuide())
	}
}

func TestDetectGitLabFromOrigin(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"glab": true, "gh": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("https://gitlab.com/group/sub/project.git\n"), nil
			}
			return []byte("{}"), nil
		},
	}

	backend, err := Detect("auto", "", runner)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Name() != "GitLab" || backend.Repository() != "group/sub/project" {
		t.Fatalf("unexpected backend: %s %s", backend.Name(), backend.Repository())
	}
}

func TestDetectRejectsSubstringHostFalsePositive(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"gh": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("https://notgithub.com/owner/repo.git\n"), nil
			}
			return nil, nil
		},
	}
	_, err := Detect("auto", "", runner)
	if err == nil || !strings.Contains(err.Error(), "cannot identify provider") {
		t.Fatalf("expected ambiguous host error, got %v", err)
	}
}

func TestEnterpriseHostIsPassedToGitLab(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"glab": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("git@gitlab.example.test:group/project.git\n"), nil
			}
			return []byte("{}"), nil
		},
	}
	backend, err := Detect("auto", "", runner)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Name() != "GitLab" {
		t.Fatalf("unexpected backend %s", backend.Name())
	}
	calls := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		calls = append(calls, strings.Join(call, " "))
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "glab auth status --hostname gitlab.example.test") || !strings.Contains(joined, "https://gitlab.example.test/group/project") {
		t.Fatalf("enterprise hostname not propagated:\n%s", joined)
	}
}

func TestGitHubClosedFilterExcludesMerged(t *testing.T) {
	fixture := `[
		{"number":1,"title":"closed","state":"closed","user":{"login":"a"},"merged_at":null},
		{"number":2,"title":"merged","state":"closed","user":{"login":"b"},"merged_at":"2026-01-01T00:00:00Z"}
	]`
	runner := &fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return []byte(fixture), nil
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	items, err := g.List(context.Background(), PullRequests, Filter{Label: "Closed", Value: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "1" {
		t.Fatalf("expected only unmerged closed PR, got %#v", items)
	}
	wantPrefix := []string{"gh", "api", "--hostname", "github.com", "--method", "GET", "repos/owner/repo/pulls"}
	if !reflect.DeepEqual(runner.calls[0][:len(wantPrefix)], wantPrefix) {
		t.Fatalf("unexpected command: %#v", runner.calls[0])
	}
}

func TestGitLabEncodesNestedProjectPath(t *testing.T) {
	runner := &fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return []byte(`[]`), nil
	}}
	g := &GitLab{runner: runner, repo: "group/sub/project", project: "group%2Fsub%2Fproject"}

	if _, err := g.List(context.Background(), Branches, Filter{Label: "All", Value: "all"}); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(runner.calls[0], " ")
	if !strings.Contains(command, "projects/group%2Fsub%2Fproject/repository/branches") {
		t.Fatalf("project path was not encoded: %s", command)
	}
}

func TestMergeCommandsCarryHeadSHAPrecondition(t *testing.T) {
	githubRunner := &fakeRunner{run: func(name string, args ...string) ([]byte, error) { return []byte(`{}`), nil }}
	github := &GitHub{runner: githubRunner, repo: "owner/repo", host: "github.com"}
	if err := github.Merge(context.Background(), Item{ID: "12", HeadSHA: "abc123"}); err != nil {
		t.Fatal(err)
	}
	if command := strings.Join(githubRunner.calls[0], " "); !strings.Contains(command, "sha=abc123") {
		t.Fatalf("GitHub merge omitted SHA: %s", command)
	}

	gitlabRunner := &fakeRunner{run: func(name string, args ...string) ([]byte, error) { return []byte(`{}`), nil }}
	gitlab := &GitLab{runner: gitlabRunner, project: "group%2Fproject", host: "gitlab.com"}
	if err := gitlab.Merge(context.Background(), Item{ID: "34", HeadSHA: "def456"}); err != nil {
		t.Fatal(err)
	}
	if command := strings.Join(gitlabRunner.calls[0], " "); !strings.Contains(command, "sha=def456") {
		t.Fatalf("GitLab merge omitted SHA: %s", command)
	}
}

func TestMergeRejectsMissingHeadSHA(t *testing.T) {
	githubRunner := &fakeRunner{}
	github := &GitHub{runner: githubRunner, repo: "owner/repo", host: "github.com"}
	if err := github.Merge(context.Background(), Item{ID: "12"}); err == nil {
		t.Fatal("GitHub merge accepted an item without a head SHA")
	}
	if len(githubRunner.calls) != 0 {
		t.Fatal("GitHub API was called despite missing head SHA")
	}

	gitlabRunner := &fakeRunner{}
	gitlab := &GitLab{runner: gitlabRunner, project: "group%2Fproject", host: "gitlab.com"}
	if err := gitlab.Merge(context.Background(), Item{ID: "34"}); err == nil {
		t.Fatal("GitLab merge accepted an item without a head SHA")
	}
	if len(gitlabRunner.calls) != 0 {
		t.Fatal("GitLab API was called despite missing head SHA")
	}
}
