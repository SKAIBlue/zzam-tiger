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

func TestDetectTreatsNonGitHubHostAsGitLab(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"glab": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("https://notgithub.com/owner/repo.git\n"), nil
			}
			return []byte("{}"), nil
		},
	}
	backend, err := Detect("auto", "", runner)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Name() != "GitLab" || backend.Repository() != "owner/repo" {
		t.Fatalf("unexpected backend: %s %s", backend.Name(), backend.Repository())
	}
	joined := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		joined = append(joined, strings.Join(call, " "))
	}
	if calls := strings.Join(joined, "\n"); !strings.Contains(calls, "glab auth status --hostname notgithub.com") {
		t.Fatalf("non-GitHub hostname was not passed to GitLab:\n%s", calls)
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
		{"number":1,"title":"closed","state":"closed","user":{"login":"a"},"assignees":[{"id":7,"login":"me"}],"merged_at":null},
		{"number":2,"title":"merged","state":"closed","user":{"login":"b"},"merged_at":"2026-01-01T00:00:00Z"}
	]`
	runner := &fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "--method GET user") {
			return []byte(`{"id":7,"login":"me"}`), nil
		}
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
	if !items[0].AssignedToMe {
		t.Fatal("pull request assigned to the current user was not marked")
	}
	wantPrefix := []string{"gh", "api", "--hostname", "github.com", "--method", "GET", "repos/owner/repo/pulls"}
	if !reflect.DeepEqual(runner.calls[1][:len(wantPrefix)], wantPrefix) {
		t.Fatalf("unexpected command: %#v", runner.calls[1])
	}
}

func TestProvidersExposeAssignedToMeFilters(t *testing.T) {
	providers := []Provider{
		&GitHub{},
		&GitLab{},
	}
	for _, backend := range providers {
		for _, kind := range []Kind{PullRequests, Issues} {
			found := false
			for _, filter := range backend.Filters(kind) {
				found = found || filter.Value == "assigned"
			}
			if !found {
				t.Fatalf("%s %s filters do not include assigned-to-me", backend.Name(), kind)
			}
		}
	}
}

func TestGitHubAssignedIssueFilterUsesCurrentLogin(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "--method GET user") {
			return []byte(`{"id":7,"login":"me"}`), nil
		}
		return []byte(`{"items":[{"number":3,"title":"mine","state":"open","user":{"login":"author"},"assignees":[{"id":7,"login":"me"}]}]}`), nil
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	items, err := g.List(context.Background(), Issues, Filter{Label: "Assigned to me", Value: "assigned"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].AssignedToMe || len(items[0].Assignees) != 1 {
		t.Fatalf("unexpected assigned issue items: %#v", items)
	}
	command := strings.Join(runner.calls[1], " ")
	if !strings.Contains(command, "search/issues") || !strings.Contains(command, "is:issue") || !strings.Contains(command, "assignee:me") {
		t.Fatalf("assigned issue search omitted type or current login: %s", command)
	}
}

func TestGitHubAssignedPullRequestSearchPreservesMergedState(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "--method GET user") {
			return []byte(`{"id":7,"login":"me"}`), nil
		}
		return []byte(`{"items":[{"number":4,"title":"merged","state":"closed","user":{"login":"author"},"pull_request":{"merged_at":"2026-01-01T00:00:00Z"}}]}`), nil
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	items, err := g.List(context.Background(), PullRequests, Filter{Label: "Assigned to me", Value: "assigned"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "merged" || !items[0].AssignedToMe || len(items[0].Assignees) != 1 {
		t.Fatalf("unexpected assigned pull requests: %#v", items)
	}
	command := strings.Join(runner.calls[1], " ")
	if !strings.Contains(command, "search/issues") || !strings.Contains(command, "is:pr") || !strings.Contains(command, "assignee:me") {
		t.Fatalf("assigned pull request search omitted type or current login: %s", command)
	}
}

func TestGitHubAssignmentCommandsAddAndRemoveOnlyCurrentUser(t *testing.T) {
	runner := &fakeRunner{}
	g := &GitHub{runner: runner, repo: "owner/repo", user: &Assignee{ID: "7", Login: "me"}}
	item := Item{ID: "12", Assignees: []Assignee{{ID: "9", Login: "other"}}}

	if err := g.SetAssigned(context.Background(), PullRequests, item, true); err != nil {
		t.Fatal(err)
	}
	item.Assignees = append(item.Assignees, Assignee{ID: "7", Login: "me"})
	if err := g.SetAssigned(context.Background(), PullRequests, item, false); err != nil {
		t.Fatal(err)
	}
	if add := strings.Join(runner.calls[0], " "); !strings.Contains(add, "--method POST repos/owner/repo/issues/12/assignees") || !strings.Contains(add, "assignees[]=me") {
		t.Fatalf("unexpected GitHub assign command: %s", add)
	}
	if remove := strings.Join(runner.calls[1], " "); !strings.Contains(remove, "--method DELETE repos/owner/repo/issues/12/assignees") || !strings.Contains(remove, "assignees[]=me") {
		t.Fatalf("unexpected GitHub unassign command: %s", remove)
	}
	if err := g.SetAssigned(context.Background(), Issues, Item{ID: "13", Assignees: []Assignee{{ID: "7", Login: "me"}}}, true); err != nil {
		t.Fatal(err)
	}
	if err := g.SetAssigned(context.Background(), Issues, Item{ID: "14"}, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("stale assignment snapshots suppressed API calls: %#v", runner.calls)
	}
}

func TestGitLabAssignedFilterAndAssignmentPreserveOtherUsers(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "api user ") {
			return []byte(`{"id":7,"username":"me"}`), nil
		}
		if strings.Contains(command, "api graphql ") {
			return []byte(`{"data":{"assignment":{"errors":[]}}}`), nil
		}
		return []byte(`[{"iid":4,"title":"mine","state":"opened","author":{"username":"author"},"assignees":[{"id":7,"username":"me"},{"id":9,"username":"other"}]}]`), nil
	}}
	g := &GitLab{runner: runner, repo: "group/project", project: "group%2Fproject"}

	items, err := g.List(context.Background(), PullRequests, Filter{Label: "Assigned to me", Value: "assigned"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].AssignedToMe {
		t.Fatalf("unexpected assigned merge requests: %#v", items)
	}
	if command := strings.Join(runner.calls[0], " "); !strings.Contains(command, "scope=assigned_to_me") || !strings.Contains(command, "state=all") {
		t.Fatalf("unexpected GitLab assigned filter: %s", command)
	}
	if err := g.SetAssigned(context.Background(), PullRequests, items[0], false); err != nil {
		t.Fatal(err)
	}
	remove := strings.Join(runner.calls[2], " ")
	if !strings.Contains(remove, "api graphql") || !strings.Contains(remove, "mergeRequestSetAssignees") || !strings.Contains(remove, "operationMode: REMOVE") || !strings.Contains(remove, `assigneeUsernames: ["me"]`) {
		t.Fatalf("GitLab unassign was not atomic: %s", remove)
	}
	if err := g.SetAssigned(context.Background(), Issues, Item{ID: "5", Assignees: []Assignee{{ID: "9", Login: "other"}}}, true); err != nil {
		t.Fatal(err)
	}
	add := strings.Join(runner.calls[3], " ")
	if !strings.Contains(add, "api graphql") || !strings.Contains(add, "issueSetAssignees") || !strings.Contains(add, "operationMode: APPEND") || !strings.Contains(add, `assigneeUsernames: ["me"]`) {
		t.Fatalf("GitLab assign was not atomic: %s", add)
	}
}

func TestGitLabAssignmentReportsGraphQLErrors(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, _ ...string) ([]byte, error) {
		return []byte(`{"data":{"assignment":{"errors":["not allowed"]}}}`), nil
	}}
	g := &GitLab{
		runner:  runner,
		repo:    "group/project",
		project: "group%2Fproject",
		user:    &Assignee{ID: "7", Login: "me"},
	}
	err := g.SetAssigned(context.Background(), Issues, Item{ID: "5"}, true)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("SetAssigned error = %v, want GraphQL mutation error", err)
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
