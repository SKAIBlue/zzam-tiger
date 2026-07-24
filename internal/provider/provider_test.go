package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func githubLogsFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, contents := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type fakeRunner struct {
	paths map[string]bool
	run   func(string, ...string) ([]byte, error)
	calls [][]string
	input [][]byte
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.run != nil {
		return f.run(name, args...)
	}
	return nil, nil
}

func (f *fakeRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.input = append(f.input, append([]byte(nil), input...))
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

func TestDetectSelfManagedGitLabFromAuthenticatedCLI(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"glab": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("https://git.example.test/owner/repo.git\n"), nil
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
	if calls := strings.Join(joined, "\n"); !strings.Contains(calls, "glab auth status --hostname git.example.test") {
		t.Fatalf("self-managed GitLab hostname was not passed to glab:\n%s", calls)
	}
}

func TestDetectExplicitProviderPreservesOriginHost(t *testing.T) {
	tests := []struct {
		name       string
		requested  string
		origin     string
		wantHost   string
		wantPrefix string
	}{
		{name: "GitHub Enterprise", requested: "github", origin: "https://github.example.test/owner/repo.git", wantHost: "github.example.test", wantPrefix: "gh auth status"},
		{name: "self-managed GitLab", requested: "gitlab", origin: "https://gitlab.example.test/owner/repo.git", wantHost: "gitlab.example.test", wantPrefix: "glab auth status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{
				paths: map[string]bool{"gh": true, "glab": true},
				run: func(name string, _ ...string) ([]byte, error) {
					if name == "git" {
						return []byte(test.origin), nil
					}
					return []byte(`{}`), nil
				},
			}
			if _, err := Detect(test.requested, "", runner); err != nil {
				t.Fatal(err)
			}
			calls := make([]string, 0, len(runner.calls))
			for _, call := range runner.calls {
				calls = append(calls, strings.Join(call, " "))
			}
			joined := strings.Join(calls, "\n")
			if !strings.Contains(joined, test.wantPrefix) || !strings.Contains(joined, "--hostname "+test.wantHost) {
				t.Fatalf("explicit provider did not preserve the origin host:\n%s", joined)
			}
		})
	}
}

func TestDetectGitHubEnterpriseFromAuthenticatedCLI(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"gh": true},
		run: func(name string, args ...string) ([]byte, error) {
			if name == "git" {
				return []byte("git@github.example.test:owner/repo.git\n"), nil
			}
			return []byte("{}"), nil
		},
	}
	backend, err := Detect("auto", "", runner)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Name() != "GitHub" || backend.Repository() != "owner/repo" {
		t.Fatalf("unexpected backend: %s %s", backend.Name(), backend.Repository())
	}
	calls := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		calls = append(calls, strings.Join(call, " "))
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "gh auth status --active --hostname github.example.test") ||
		!strings.Contains(joined, "gh repo view github.example.test/owner/repo") {
		t.Fatalf("GitHub Enterprise hostname not propagated:\n%s", joined)
	}
}

func TestDetectUnknownSelfHostedProviderRequestsOverride(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"gh": true, "glab": true},
		run: func(name string, _ ...string) ([]byte, error) {
			if name == "git" {
				return []byte("https://git.example.test/owner/repo.git\n"), nil
			}
			return nil, errors.New("not authenticated")
		},
	}
	_, err := Detect("auto", "", runner)
	if err == nil || !strings.Contains(err.Error(), "pass --provider github or --provider gitlab") {
		t.Fatalf("expected actionable provider override error, got %v", err)
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

func TestGitHubAssignedIssueFilterOnlyRequestsOpenItems(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "--method GET user") {
			return []byte(`{"id":7,"login":"me"}`), nil
		}
		return []byte(`{"items":[{"number":3,"title":"open","state":"open","user":{"login":"author"},"assignees":[{"id":7,"login":"me"}]}]}`), nil
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
	if !strings.Contains(command, "search/issues") || !strings.Contains(command, "is:issue") || !strings.Contains(command, "is:open") || !strings.Contains(command, "assignee:me") {
		t.Fatalf("assigned issue search omitted type, open state, or current login: %s", command)
	}
}

func TestGitHubAssignedPullRequestFilterOnlyRequestsOpenItems(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "--method GET user") {
			return []byte(`{"id":7,"login":"me"}`), nil
		}
		return []byte(`{"items":[{"number":4,"title":"open","state":"open","user":{"login":"author"}}]}`), nil
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	items, err := g.List(context.Background(), PullRequests, Filter{Label: "Assigned to me", Value: "assigned"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "open" || !items[0].AssignedToMe || len(items[0].Assignees) != 1 {
		t.Fatalf("unexpected assigned pull requests: %#v", items)
	}
	command := strings.Join(runner.calls[1], " ")
	if !strings.Contains(command, "search/issues") || !strings.Contains(command, "is:pr") || !strings.Contains(command, "is:open") || !strings.Contains(command, "assignee:me") {
		t.Fatalf("assigned pull request search omitted type, open state, or current login: %s", command)
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
	if command := strings.Join(runner.calls[0], " "); !strings.Contains(command, "scope=assigned_to_me") || !strings.Contains(command, "state=opened") {
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

func TestParseUnifiedDiffLinesTracksOldAndNewLineNumbers(t *testing.T) {
	t.Parallel()
	patch := "@@ -2,3 +2,4 @@ func example() {\n context\n-old\n+new\n+added\n@@ -10 +11 @@\n-last\n+next\n\\ No newline at end of file"
	want := []DiffLine{
		{OldLine: 2, NewLine: 2, OldPosition: 2, NewPosition: 2, Position: 1, Text: " context"},
		{OldLine: 3, OldPosition: 3, NewPosition: 3, Position: 2, Text: "-old"},
		{NewLine: 3, OldPosition: 4, NewPosition: 3, Position: 3, Text: "+new"},
		{NewLine: 4, OldPosition: 4, NewPosition: 4, Position: 4, Text: "+added"},
		{OldLine: 10, OldPosition: 10, NewPosition: 11, Position: 6, Text: "-last"},
		{NewLine: 11, OldPosition: 11, NewPosition: 11, Position: 7, Text: "+next"},
	}
	if got := ParseUnifiedDiffLines(patch); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseUnifiedDiffLines() = %#v, want %#v", got, want)
	}
}

func TestGitHubPullRequestDetailIncludesDiffs(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "pulls/12/files?per_page=100"):
			return []byte(`[[{"filename":"new.go","previous_filename":"old.go","patch":"@@ -4,2 +4,2 @@\n-old\n+new\n same"}],[{"filename":"large.bin"}]]`), nil
		case strings.Contains(command, "pulls/12/reviews"), strings.Contains(command, "pulls/12/comments"), strings.Contains(command, "issues/12/comments"), strings.Contains(command, "issues/12/timeline"):
			return []byte(`[]`), nil
		case strings.Contains(command, "--method GET user"):
			return []byte(`{"id":7,"login":"me"}`), nil
		default:
			return []byte(`{"number":12,"title":"change","state":"open","user":{"login":"author"},"head":{"sha":"head123"},"base":{"sha":"base123"}}`), nil
		}
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	detail, err := g.Detail(context.Background(), PullRequests, Item{ID: "12"})
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffFile{
		{OldPath: "old.go", NewPath: "new.go", HeadSHA: "head123", BaseSHA: "base123", Lines: []DiffLine{{OldLine: 4, OldPosition: 4, NewPosition: 4, Position: 1, Text: "-old"}, {NewLine: 4, OldPosition: 5, NewPosition: 4, Position: 2, Text: "+new"}, {OldLine: 5, NewLine: 5, OldPosition: 5, NewPosition: 5, Position: 3, Text: " same"}}},
		{OldPath: "large.bin", NewPath: "large.bin", HeadSHA: "head123", BaseSHA: "base123", TooLarge: true},
	}
	if !reflect.DeepEqual(detail.Diffs, want) {
		t.Fatalf("Detail().Diffs = %#v, want %#v", detail.Diffs, want)
	}
	var filesCommand string
	for _, call := range runner.calls {
		command := strings.Join(call, " ")
		if strings.Contains(command, "pulls/12/files?per_page=100") {
			filesCommand = command
			break
		}
	}
	if !strings.Contains(filesCommand, "--paginate") || !strings.Contains(filesCommand, "--slurp") {
		t.Fatalf("GitHub diff request was not paginated and slurped: %s", filesCommand)
	}
}

func TestGitLabMergeRequestDetailIncludesVersionedDiffs(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "merge_requests/34/diffs?per_page=100&unidiff=true"):
			return []byte(`[{"old_path":"old.go","new_path":"new.go","diff":"@@ -8 +8 @@\n-old\n+new"},{"old_path":"huge","new_path":"huge","too_large":true}]`), nil
		case strings.Contains(command, "merge_requests/34/versions?per_page=1"):
			return []byte(`[{"base_commit_sha":"base123","start_commit_sha":"start123","head_commit_sha":"head123"}]`), nil
		case strings.Contains(command, "merge_requests/34/discussions"):
			return []byte(`[]`), nil
		case strings.Contains(command, "api user "):
			return []byte(`{"id":7,"username":"me"}`), nil
		default:
			return []byte(`{"iid":34,"title":"change","state":"opened","author":{"username":"author"}}`), nil
		}
	}}
	g := &GitLab{runner: runner, repo: "group/project", project: "group%2Fproject"}

	detail, err := g.Detail(context.Background(), PullRequests, Item{ID: "34"})
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffFile{
		{OldPath: "old.go", NewPath: "new.go", BaseSHA: "base123", StartSHA: "start123", HeadSHA: "head123", Lines: []DiffLine{{OldLine: 8, OldPosition: 8, NewPosition: 8, Position: 1, Text: "-old"}, {NewLine: 8, OldPosition: 9, NewPosition: 8, Position: 2, Text: "+new"}}},
		{OldPath: "huge", NewPath: "huge", BaseSHA: "base123", StartSHA: "start123", HeadSHA: "head123", TooLarge: true},
	}
	if !reflect.DeepEqual(detail.Diffs, want) {
		t.Fatalf("Detail().Diffs = %#v, want %#v", detail.Diffs, want)
	}
	var diffsCommand string
	for _, call := range runner.calls {
		command := strings.Join(call, " ")
		if strings.Contains(command, "merge_requests/34/diffs?per_page=100&unidiff=true") {
			diffsCommand = command
			break
		}
	}
	if !strings.Contains(diffsCommand, "--paginate") || !strings.Contains(diffsCommand, "--output json") {
		t.Fatalf("GitLab diff request was not paginated JSON: %s", diffsCommand)
	}
}

func TestGitHubCommitDetailIncludesDiffAndInlineComments(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "commits/abc123/comments") {
			return []byte(`[{"body":"Check this line","path":"new.go","position":2,"line":4,"created_at":"2026-07-23T01:00:00Z","user":{"login":"reviewer"}}]`), nil
		}
		return []byte(`{"sha":"abc123","html_url":"https://github.com/owner/repo/commit/abc123","commit":{"message":"change behavior","author":{"name":"Author","date":"2026-07-23T00:00:00Z"}},"parents":[{"sha":"parent123"}],"stats":{"additions":1,"deletions":1,"total":2},"files":[{"filename":"new.go","previous_filename":"old.go","status":"renamed","changes":2,"patch":"@@ -4 +4 @@\n-old\n+new"}]}`), nil
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	detail, err := g.Detail(context.Background(), Commits, Item{ID: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Diffs) != 1 || detail.Diffs[0].OldPath != "old.go" || detail.Diffs[0].NewPath != "new.go" || detail.Diffs[0].BaseSHA != "parent123" || detail.Diffs[0].HeadSHA != "abc123" {
		t.Fatalf("unexpected commit diff: %#v", detail.Diffs)
	}
	if got := detail.Diffs[0].Lines; len(got) != 2 || got[1].Position != 2 || got[1].NewLine != 4 {
		t.Fatalf("unexpected commit diff lines: %#v", got)
	}
	if got := detail.Diffs[0].Reviews; len(got) != 1 || got[0].Body != "Check this line" || got[0].NewLine != 4 || got[0].Replyable || got[0].Resolvable {
		t.Fatalf("unexpected inline commit comments: %#v", got)
	}
	if len(detail.Sections) != 2 {
		t.Fatalf("positioned comment unexpectedly fell back to a section: %#v", detail.Sections)
	}
}

func TestGitLabCommitDetailIncludesDiffAndInlineComments(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "commits/abc123/diff?per_page=100&unidiff=true"):
			return []byte(`[{"old_path":"old.go","new_path":"new.go","renamed_file":true,"diff":"@@ -4 +4 @@\n-old\n+new"}]`), nil
		case strings.Contains(command, "commits/abc123/comments?per_page=100"):
			return []byte(`[{"note":"Check this line","path":"new.go","line":4,"line_type":"new","created_at":"2026-07-23T01:00:00Z","author":{"username":"reviewer"}}]`), nil
		default:
			return []byte(`{"id":"abc123","title":"change behavior","message":"change behavior","author_name":"Author","committed_date":"2026-07-23T00:00:00Z","stats":{"additions":1,"deletions":1,"total":2}}`), nil
		}
	}}
	g := &GitLab{runner: runner, repo: "group/project", project: "group%2Fproject"}

	detail, err := g.Detail(context.Background(), Commits, Item{ID: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Diffs) != 1 || detail.Diffs[0].OldPath != "old.go" || detail.Diffs[0].NewPath != "new.go" || detail.Diffs[0].HeadSHA != "abc123" {
		t.Fatalf("unexpected commit diff: %#v", detail.Diffs)
	}
	if got := detail.Diffs[0].Reviews; len(got) != 1 || got[0].Body != "Check this line" || got[0].NewLine != 4 || got[0].Replyable || got[0].Resolvable {
		t.Fatalf("unexpected inline commit comments: %#v", got)
	}
	if len(detail.Sections) != 2 {
		t.Fatalf("positioned comment unexpectedly fell back to a section: %#v", detail.Sections)
	}
}

func TestProvidersCreateCommitComments(t *testing.T) {
	githubRunner := &fakeRunner{}
	github := &GitHub{runner: githubRunner, repo: "owner/repo"}
	item := Item{ID: "abc123", State: "commit"}
	if err := github.AddComment(context.Background(), Commits, item, "overall"); err != nil {
		t.Fatal(err)
	}
	if err := github.AddCommitComment(context.Background(), item, ReviewTarget{NewPath: "new.go", NewLine: 4, Position: 2, Side: ReviewSideNew}, "inline"); err != nil {
		t.Fatal(err)
	}
	githubCommands := strings.Join([]string{strings.Join(githubRunner.calls[0], " "), strings.Join(githubRunner.calls[1], " ")}, "\n")
	for _, want := range []string{"commits/abc123/comments -f body=overall", "-f path=new.go", "-F position=2"} {
		if !strings.Contains(githubCommands, want) {
			t.Fatalf("GitHub commit comment commands omitted %q:\n%s", want, githubCommands)
		}
	}

	gitlabRunner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "-f line_type=old") {
			return []byte(`{"note":"inline","path":"old.go","line":4,"line_type":"old"}`), nil
		}
		return []byte(`{}`), nil
	}}
	gitlab := &GitLab{runner: gitlabRunner, repo: "group/project", project: "group%2Fproject"}
	if err := gitlab.AddComment(context.Background(), Commits, item, "overall"); err != nil {
		t.Fatal(err)
	}
	if err := gitlab.AddCommitComment(context.Background(), item, ReviewTarget{OldPath: "old.go", OldLine: 4, Side: ReviewSideOld}, "inline"); err != nil {
		t.Fatal(err)
	}
	gitlabCommands := strings.Join([]string{strings.Join(gitlabRunner.calls[0], " "), strings.Join(gitlabRunner.calls[1], " ")}, "\n")
	for _, want := range []string{"commits/abc123/comments", "-f note=overall", "-f path=old.go", "-f line=4", "-f line_type=old"} {
		if !strings.Contains(gitlabCommands, want) {
			t.Fatalf("GitLab commit comment commands omitted %q:\n%s", want, gitlabCommands)
		}
	}

	rangeTarget := ReviewTarget{NewPath: "new.go", StartNewLine: 3, NewLine: 4, Side: ReviewSideNew, Position: 2}
	if err := github.AddCommitComment(context.Background(), item, rangeTarget, "range"); err == nil || !strings.Contains(err.Error(), "one diff line") {
		t.Fatalf("GitHub range commit comment error = %v", err)
	}
	invalidGitLab := &GitLab{runner: &fakeRunner{run: func(_ string, _ ...string) ([]byte, error) { return []byte(`{}`), nil }}, repo: "group/project", project: "group%2Fproject"}
	if err := invalidGitLab.AddCommitComment(context.Background(), item, ReviewTarget{NewPath: "new.go", NewLine: 4, Side: ReviewSideNew}, "inline"); err == nil || !strings.Contains(err.Error(), "without the requested") {
		t.Fatalf("GitLab silently accepted an unanchored commit comment: %v", err)
	}
}

func TestGitHubCommentCommandsAndValidation(t *testing.T) {
	runner := &fakeRunner{}
	g := &GitHub{runner: runner, repo: "owner/repo"}
	if err := g.AddComment(context.Background(), Issues, Item{ID: "7"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if command := strings.Join(runner.calls[0], " "); !strings.Contains(command, "--method POST repos/owner/repo/issues/7/comments") || !strings.Contains(command, "-f body=hello") {
		t.Fatalf("unexpected general comment command: %s", command)
	}
	target := ReviewTarget{OldPath: "old.go", NewPath: "new.go", NewLine: 14, HeadSHA: "head123"}
	if err := g.AddReviewComment(context.Background(), Item{ID: "12"}, target, "review"); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(runner.calls[1], " ")
	for _, part := range []string{"--method POST repos/owner/repo/pulls/12/comments", "-f body=review", "-f commit_id=head123", "-f path=new.go", "-F line=14", "-f side=RIGHT"} {
		if !strings.Contains(command, part) {
			t.Fatalf("GitHub inline comment omitted %q: %s", part, command)
		}
	}
	leftTarget := ReviewTarget{OldPath: "old.go", NewPath: "renamed.go", OldLine: 22, HeadSHA: "head123"}
	if err := g.AddReviewComment(context.Background(), Item{ID: "12"}, leftTarget, "deletion"); err != nil {
		t.Fatal(err)
	}
	leftCommand := strings.Join(runner.calls[2], " ")
	for _, part := range []string{"-f path=renamed.go", "-F line=22", "-f side=LEFT"} {
		if !strings.Contains(leftCommand, part) {
			t.Fatalf("GitHub renamed-file deletion omitted %q: %s", part, leftCommand)
		}
	}
	rangeTarget := ReviewTarget{OldPath: "old.go", NewPath: "new.go", StartOldLine: 11, StartNewLine: 12, OldLine: 13, NewLine: 14, Side: ReviewSideOld, HeadSHA: "head123"}
	if err := g.AddReviewComment(context.Background(), Item{ID: "12"}, rangeTarget, "range"); err != nil {
		t.Fatal(err)
	}
	rangeCommand := strings.Join(runner.calls[3], " ")
	for _, part := range []string{"-F start_line=11", "-f start_side=LEFT", "-F line=13", "-f side=LEFT"} {
		if !strings.Contains(rangeCommand, part) {
			t.Fatalf("GitHub range comment omitted %q: %s", part, rangeCommand)
		}
	}
	for _, test := range []struct {
		name   string
		item   Item
		target ReviewTarget
		body   string
	}{
		{"empty body", Item{ID: "12"}, target, "  "},
		{"missing item", Item{}, target, "body"},
		{"missing line", Item{ID: "12"}, ReviewTarget{NewPath: "new.go", HeadSHA: "head"}, "body"},
		{"missing head", Item{ID: "12"}, ReviewTarget{NewPath: "new.go", NewLine: 1}, "body"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := g.AddReviewComment(context.Background(), test.item, test.target, test.body); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if len(runner.calls) != 4 {
		t.Fatalf("validation issued API calls: %#v", runner.calls[4:])
	}
}

func TestGitLabCommentCommandsAndValidation(t *testing.T) {
	runner := &fakeRunner{}
	g := &GitLab{runner: runner, project: "group%2Fproject"}
	if err := g.AddComment(context.Background(), PullRequests, Item{ID: "34"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if command := strings.Join(runner.calls[0], " "); !strings.Contains(command, "--method POST") || !strings.Contains(command, "merge_requests/34/notes") || !strings.Contains(command, "-f body=hello") {
		t.Fatalf("unexpected general comment command: %s", command)
	}
	target := ReviewTarget{OldPath: "old.go", NewPath: "new.go", OldLine: 9, NewLine: 10, BaseSHA: "base", StartSHA: "start", HeadSHA: "head"}
	bodies := []string{"@/etc/passwd", "line one\n[position][head_sha]=attacker"}
	for _, body := range bodies {
		if err := g.AddReviewComment(context.Background(), Item{ID: "34"}, target, body); err != nil {
			t.Fatal(err)
		}
		callIndex := len(runner.calls) - 1
		command := strings.Join(runner.calls[callIndex], " ")
		if !strings.Contains(command, "merge_requests/34/discussions") || !strings.Contains(command, "--input -") {
			t.Fatalf("GitLab inline comment did not use stdin JSON: %s", command)
		}
		if !strings.Contains(command, "--header Content-Type: application/json") {
			t.Fatalf("GitLab inline comment did not declare its JSON content type: %s", command)
		}
		if strings.Contains(command, body) || strings.Contains(command, "--form") {
			t.Fatalf("GitLab inline comment exposed user body in arguments: %s", command)
		}
		var payload struct {
			Body     string `json:"body"`
			Position struct {
				PositionType string `json:"position_type"`
				OldPath      string `json:"old_path"`
				NewPath      string `json:"new_path"`
				OldLine      int    `json:"old_line"`
				NewLine      int    `json:"new_line"`
				BaseSHA      string `json:"base_sha"`
				StartSHA     string `json:"start_sha"`
				HeadSHA      string `json:"head_sha"`
			} `json:"position"`
		}
		if err := json.Unmarshal(runner.input[callIndex-1], &payload); err != nil {
			t.Fatalf("decode stdin payload: %v", err)
		}
		if payload.Body != body || payload.Position.PositionType != "text" || payload.Position.OldPath != "old.go" || payload.Position.NewPath != "new.go" || payload.Position.OldLine != 9 || payload.Position.NewLine != 10 || payload.Position.BaseSHA != "base" || payload.Position.StartSHA != "start" || payload.Position.HeadSHA != "head" {
			t.Fatalf("unexpected stdin payload: %#v", payload)
		}
	}
	rangeTarget := ReviewTarget{
		OldPath:          "old.go",
		NewPath:          "new.go",
		StartNewLine:     10,
		NewLine:          11,
		StartOldPosition: 9,
		StartNewPosition: 10,
		OldPosition:      9,
		NewPosition:      11,
		Side:             ReviewSideNew,
		BaseSHA:          "base",
		StartSHA:         "start",
		HeadSHA:          "head",
	}
	if err := g.AddReviewComment(context.Background(), Item{ID: "34"}, rangeTarget, "range"); err != nil {
		t.Fatal(err)
	}
	var rangePayload struct {
		Position struct {
			OldLine   *int `json:"old_line"`
			NewLine   *int `json:"new_line"`
			LineRange struct {
				Start struct {
					LineCode string `json:"line_code"`
					Type     string `json:"type"`
					OldLine  *int   `json:"old_line"`
					NewLine  *int   `json:"new_line"`
				} `json:"start"`
				End struct {
					LineCode string `json:"line_code"`
					Type     string `json:"type"`
					OldLine  *int   `json:"old_line"`
					NewLine  *int   `json:"new_line"`
				} `json:"end"`
			} `json:"line_range"`
		} `json:"position"`
	}
	if err := json.Unmarshal(runner.input[2], &rangePayload); err != nil {
		t.Fatalf("decode range payload: %v", err)
	}
	start, end := rangePayload.Position.LineRange.Start, rangePayload.Position.LineRange.End
	if rangePayload.Position.OldLine != nil || rangePayload.Position.NewLine == nil || *rangePayload.Position.NewLine != 11 {
		t.Fatalf("unexpected top-level range end: %#v", rangePayload.Position)
	}
	if start.LineCode != "5b3e52a404c3d151e3c7765cb2946a669a974528_9_10" || start.Type != "new" || start.OldLine != nil || start.NewLine == nil || *start.NewLine != 10 {
		t.Fatalf("unexpected range start: %#v", start)
	}
	if end.LineCode != "5b3e52a404c3d151e3c7765cb2946a669a974528_9_11" || end.Type != "new" || end.OldLine != nil || end.NewLine == nil || *end.NewLine != 11 {
		t.Fatalf("unexpected range end: %#v", end)
	}
	if err := g.AddReviewComment(context.Background(), Item{ID: "34"}, ReviewTarget{OldPath: "old.go", NewPath: "new.go", NewLine: 1, BaseSHA: "base", StartSHA: "", HeadSHA: "head"}, "review"); err == nil {
		t.Fatal("expected validation error for incomplete diff version")
	}
	if len(runner.calls) != 4 {
		t.Fatalf("validation issued API calls: %#v", runner.calls[4:])
	}
}

func TestGitHubRightSideRangeCommentPayload(t *testing.T) {
	runner := &fakeRunner{}
	g := &GitHub{runner: runner, repo: "owner/repo"}
	target := ReviewTarget{
		OldPath:      "old.go",
		NewPath:      "renamed.go",
		StartOldLine: 20,
		StartNewLine: 21,
		OldLine:      22,
		NewLine:      23,
		Side:         ReviewSideNew,
		HeadSHA:      "head123",
	}

	if err := g.AddReviewComment(context.Background(), Item{ID: "12"}, target, "right range"); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(runner.calls[0], " ")
	for _, part := range []string{"-f path=renamed.go", "-F start_line=21", "-f start_side=RIGHT", "-F line=23", "-f side=RIGHT"} {
		if !strings.Contains(command, part) {
			t.Fatalf("GitHub RIGHT range omitted %q: %s", part, command)
		}
	}
}

func TestGitLabOldContextRangeUsesRenamedPathLineCodes(t *testing.T) {
	runner := &fakeRunner{}
	g := &GitLab{runner: runner, project: "group%2Fproject"}
	target := ReviewTarget{
		OldPath:          "old.go",
		NewPath:          "renamed.go",
		StartOldLine:     20,
		StartNewLine:     21,
		OldLine:          22,
		NewLine:          23,
		StartOldPosition: 30,
		StartNewPosition: 40,
		OldPosition:      32,
		NewPosition:      42,
		Side:             ReviewSideOld,
		BaseSHA:          "base",
		StartSHA:         "start",
		HeadSHA:          "head",
	}

	if err := g.AddReviewComment(context.Background(), Item{ID: "34"}, target, "old context range"); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Position struct {
			OldLine int `json:"old_line"`
			NewLine int `json:"new_line"`
			Range   struct {
				Start gitlabReviewLine `json:"start"`
				End   gitlabReviewLine `json:"end"`
			} `json:"line_range"`
		} `json:"position"`
	}
	if err := json.Unmarshal(runner.input[0], &payload); err != nil {
		t.Fatalf("decode old range payload: %v", err)
	}
	if payload.Position.OldLine != 22 || payload.Position.NewLine != 23 {
		t.Fatalf("top-level position is not the context range end: %#v", payload.Position)
	}
	wantStart := gitlabReviewLine{LineCode: "640a7f34b528dad7a4926c3f7d3c857504b162a1_30_40", Type: "old", OldLine: 20, NewLine: 21}
	wantEnd := gitlabReviewLine{LineCode: "640a7f34b528dad7a4926c3f7d3c857504b162a1_32_42", Type: "old", OldLine: 22, NewLine: 23}
	if payload.Position.Range.Start != wantStart || payload.Position.Range.End != wantEnd {
		t.Fatalf("renamed-path context range = %#v, want start=%#v end=%#v", payload.Position.Range, wantStart, wantEnd)
	}
}

func TestGitHubDetailAttachesPositionedReviewCommentsToDiff(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "pulls/12/files?per_page=100"):
			return []byte(`[[{"filename":"main.go","patch":"@@ -5 +5 @@\n-old\n+new"}]]`), nil
		case strings.Contains(command, "pulls/12/comments"):
			return []byte(`[
				{"body":"current","user":{"login":"alice"},"created_at":"2026-07-20T10:00:00Z","path":"main.go","line":5,"side":"RIGHT"},
				{"body":"outdated","user":{"login":"bob"},"created_at":"2026-07-20T11:00:00Z","path":"main.go","line":null,"original_line":5,"side":"LEFT"},
				{"body":"whole file","user":{"login":"dana"},"created_at":"2026-07-20T11:30:00Z","path":"main.go","line":null,"subject_type":"file"},
				{"body":"unplaced","user":{"login":"carol"},"created_at":"2026-07-20T12:00:00Z","path":"gone.go","line":null}
			]`), nil
		case strings.Contains(command, "pulls/12/reviews"), strings.Contains(command, "issues/12/comments"), strings.Contains(command, "issues/12/timeline"):
			return []byte(`[]`), nil
		case strings.Contains(command, "reviewThreads(first:100"):
			return []byte(`[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`), nil
		case strings.Contains(command, "--method GET user"):
			return []byte(`{"id":7,"login":"me"}`), nil
		default:
			return []byte(`{"number":12,"title":"change","state":"open","user":{"login":"author"},"head":{"sha":"head"},"base":{"sha":"base"}}`), nil
		}
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}

	detail, err := g.Detail(context.Background(), PullRequests, Item{ID: "12"})
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffReview{
		{Author: "alice", Body: "current", CreatedAt: parseTime("2026-07-20T10:00:00Z"), NewLine: 5, Side: ReviewSideNew},
		{Author: "bob", Body: "outdated", CreatedAt: parseTime("2026-07-20T11:00:00Z"), OldLine: 5, Side: ReviewSideOld, Outdated: true},
		{Author: "dana", Body: "whole file", CreatedAt: parseTime("2026-07-20T11:30:00Z"), Side: ReviewSideNew, FileLevel: true},
	}
	if len(detail.Diffs) != 1 || !reflect.DeepEqual(detail.Diffs[0].Reviews, want) {
		t.Fatalf("attached GitHub reviews = %#v, want %#v", detail.Diffs, want)
	}
	foundFallback := false
	for _, section := range detail.Sections {
		foundFallback = foundFallback || section.Markdown == "unplaced"
	}
	if !foundFallback {
		t.Fatal("unpositioned GitHub review comment was not preserved as a detail section")
	}
}

func TestGitHubReviewCommentsCollectPaginatedPages(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if !strings.Contains(command, "--paginate --slurp") {
			t.Fatalf("GitHub review comments were not paginated: %s", command)
		}
		if strings.Contains(command, "reviewThreads(first:100") {
			return []byte(`[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`), nil
		}
		return []byte(`[
			[{"body":"first","user":{"login":"alice"},"path":"main.go","line":1,"side":"RIGHT"}],
			[{"body":"second","user":{"login":"bob"},"path":"main.go","line":2,"side":"RIGHT"}]
		]`), nil
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}
	reviews, err := g.githubReviewComments(context.Background(), "12")
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 || reviews[0].Review.Body != "first" || reviews[1].Review.Body != "second" {
		t.Fatalf("paginated GitHub reviews = %#v", reviews)
	}
}

func TestGitHubReviewThreadsSupportRepliesAndResolve(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "pulls/12/comments?per_page=100"):
			return []byte(`[[
				{"id":10,"body":"root","user":{"login":"alice"},"path":"main.go","line":5,"side":"RIGHT"},
				{"id":11,"in_reply_to_id":10,"body":"reply","user":{"login":"bob"},"path":"main.go","line":5,"side":"RIGHT"}
			]]`), nil
		case strings.Contains(command, "reviewThreads(first:100"):
			return []byte(`[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"THREAD_1","isResolved":false,"viewerCanResolve":true,"viewerCanReply":true,"comments":{"nodes":[{"databaseId":10},{"databaseId":11}]}}]}}}}}]`), nil
		default:
			return []byte(`{}`), nil
		}
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}
	reviews, err := g.githubReviewComments(context.Background(), "12")
	if err != nil {
		t.Fatal(err)
	}
	for index, review := range reviews {
		if review.Review.ThreadID != "THREAD_1" || review.Review.ReplyToID != "10" || !review.Review.Resolvable || !review.Review.Replyable {
			t.Fatalf("review %d missing thread metadata: %#v", index, review.Review)
		}
	}
	target := ReviewThreadTarget{ThreadID: "THREAD_1", ReplyToID: "10"}
	if err := g.AddReviewReply(context.Background(), Item{ID: "12"}, target, "follow up"); err != nil {
		t.Fatal(err)
	}
	if err := g.ResolveReview(context.Background(), Item{ID: "12"}, target); err != nil {
		t.Fatal(err)
	}
	replyCommand := strings.Join(runner.calls[len(runner.calls)-2], " ")
	resolveCommand := strings.Join(runner.calls[len(runner.calls)-1], " ")
	if !strings.Contains(replyCommand, "pulls/12/comments/10/replies") || !strings.Contains(replyCommand, "body=follow up") {
		t.Fatalf("unexpected GitHub reply command: %s", replyCommand)
	}
	if !strings.Contains(resolveCommand, "resolveReviewThread") || !strings.Contains(resolveCommand, "threadId=THREAD_1") {
		t.Fatalf("unexpected GitHub resolve command: %s", resolveCommand)
	}
}

func TestGitHubReviewThreadCapabilitiesAreRequiredAndRespected(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "pulls/12/comments?per_page=100") {
			return []byte(`[[{"id":10,"body":"root","user":{"login":"alice"},"path":"main.go","line":5,"side":"RIGHT"}]]`), nil
		}
		return nil, errors.New("graphql unavailable")
	}}
	g := &GitHub{runner: runner, repo: "owner/repo"}
	if _, err := g.githubReviewComments(context.Background(), "12"); err == nil || !strings.Contains(err.Error(), "review thread capabilities") {
		t.Fatalf("missing GitHub thread capabilities were silently ignored: %v", err)
	}

	comments := []githubReviewComment{{ID: 10, Body: "root", Path: "main.go", Line: intPointer(5), Side: "RIGHT"}}
	reviews := githubPositionedReviews(comments, map[int64]githubReviewThreadInfo{10: {ID: "THREAD_1", CanReply: false}})
	if len(reviews) != 1 || reviews[0].Review.Replyable {
		t.Fatalf("viewerCanReply=false was not preserved: %#v", reviews)
	}
}

func intPointer(value int) *int { return &value }

func TestProvidersExposeCIRuns(t *testing.T) {
	t.Parallel()
	github := &GitHub{}
	gitlab := &GitLab{}
	if CIRuns.String() != "CI Runs" || github.TabName(CIRuns) != "Actions" || gitlab.TabName(CIRuns) != "Pipelines" {
		t.Fatalf("unexpected CI run names: kind=%q github=%q gitlab=%q", CIRuns, github.TabName(CIRuns), gitlab.TabName(CIRuns))
	}
	if github.Filters(CIRuns)[0].Value != "all" || gitlab.Filters(CIRuns)[0].Value != "all" {
		t.Fatal("CI run filters must default to all runs")
	}
}

func TestCILogMarkdownIsCodeFormattedAndBounded(t *testing.T) {
	markdown := ciLogMarkdown([]byte("```\n# heading\n"))
	if markdown != "    ```\n    # heading\n    " {
		t.Fatalf("log was not rendered as an indented code block: %q", markdown)
	}
	large := bytes.Repeat([]byte("x"), maxCILogBytes+1)
	maxWrappedBytes := maxCILogBytes + (maxCILogBytes/maxCILogLineRunes+1)*5 + 100
	if bounded := ciLogMarkdown(large); !strings.Contains(bounded, "Log truncated after 2 MiB") || len(bounded) > maxWrappedBytes {
		t.Fatalf("large log was not bounded: output bytes=%d", len(bounded))
	}
	unsafe := "\x1b]52;c;YXR0YWNr\a\x1b[31mred\x1b[0m\b\x7f\x00"
	if sanitized := ciLogMarkdown([]byte(unsafe)); sanitized != "    red" {
		t.Fatalf("terminal control sequences remained in CI log: %q", sanitized)
	}
	longLine := strings.Repeat("x", maxCILogLineRunes+1)
	if wrapped := ciLogMarkdown([]byte(longLine)); wrapped != "    "+strings.Repeat("x", maxCILogLineRunes)+"\n    x" {
		t.Fatalf("long CI log line was not wrapped: %q", wrapped)
	}
}

func TestGitHubCIRunsListDetailLogsAndActions(t *testing.T) {
	logs := githubLogsFixture(t, map[string]string{"build/1_build.txt": "compile\ntests passed\n"})
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "actions/runs/42/logs"):
			return logs, nil
		case strings.Contains(command, "actions/runs/42/jobs"):
			return []byte(`{"jobs":[{"name":"build","status":"completed","conclusion":"success","steps":[{"name":"test","status":"completed","conclusion":"success"}]}]}`), nil
		case strings.Contains(command, "actions/runs/42") && strings.Contains(command, "--method GET"):
			return []byte(`{"id":42,"name":"CI","display_title":"fix build","status":"completed","conclusion":"success","event":"push","head_branch":"main","head_sha":"abcdef123456","run_attempt":2,"updated_at":"2026-07-22T01:02:03Z","html_url":"https://github.com/o/r/actions/runs/42","actor":{"login":"alice"}}`), nil
		case strings.Contains(command, "actions/runs") && strings.Contains(command, "--method GET"):
			return []byte(`{"workflow_runs":[{"id":42,"name":"CI","display_title":"fix build","status":"completed","conclusion":"success","event":"push","head_branch":"main","head_sha":"abcdef123456","run_attempt":2,"updated_at":"2026-07-22T01:02:03Z","html_url":"https://github.com/o/r/actions/runs/42","actor":{"login":"alice"}}]}`), nil
		default:
			return []byte(`{}`), nil
		}
	}}
	g := &GitHub{runner: runner, repo: "o/r"}
	items, err := g.List(context.Background(), CIRuns, Filter{Value: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "42" || items[0].Title != "fix build" || items[0].State != "completed/success" || items[0].Author != "alice" || !strings.Contains(items[0].Meta, "attempt 2") {
		t.Fatalf("unexpected GitHub run: %#v", items)
	}
	if command := strings.Join(runner.calls[0], " "); !strings.Contains(command, "repos/o/r/actions/runs") || !strings.Contains(command, "status=success") {
		t.Fatalf("unexpected GitHub run list command: %s", command)
	}
	detail, err := g.Detail(context.Background(), CIRuns, items[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Sections) != 2 || detail.Sections[0].Title != "Jobs" || !strings.Contains(detail.Sections[0].Markdown, "test · completed/success") || detail.Sections[1].Title != "Logs · build/1_build.txt" || detail.Sections[1].Markdown != "    compile\n    tests passed\n    " {
		t.Fatalf("unexpected GitHub run logs: %#v", detail.Sections)
	}
	if err := g.CancelRun(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	if err := g.Rerun(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	cancel := strings.Join(runner.calls[len(runner.calls)-2], " ")
	rerun := strings.Join(runner.calls[len(runner.calls)-1], " ")
	if !strings.Contains(cancel, "--method POST repos/o/r/actions/runs/42/cancel") || !strings.Contains(rerun, "--method POST repos/o/r/actions/runs/42/rerun") {
		t.Fatalf("unexpected GitHub run actions:\n%s\n%s", cancel, rerun)
	}
}

func TestGitLabCIRunsListDetailLogsAndActions(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "/jobs/99/trace"):
			return []byte("go test ./...\nok\n"), nil
		case strings.Contains(command, "/pipelines/7/jobs"):
			return []byte(`[{"id":99,"name":"test","stage":"verify","status":"success","started_at":"2026-07-22T01:01:00Z"},{"id":100,"name":"deploy","stage":"release","status":"manual"}]`), nil
		case strings.Contains(command, "/pipelines/7") && strings.Contains(command, "--method GET"):
			return []byte(`{"id":7,"iid":3,"name":"validate","status":"success","source":"push","ref":"main","sha":"abcdef123456","web_url":"https://gitlab.com/g/p/-/pipelines/7","updated_at":"2026-07-22T01:02:03Z","user":{"username":"bob"}}`), nil
		case strings.Contains(command, "/pipelines?"):
			return []byte(`[{"id":7,"iid":3,"name":"validate","status":"success","source":"push","ref":"main","sha":"abcdef123456","web_url":"https://gitlab.com/g/p/-/pipelines/7","updated_at":"2026-07-22T01:02:03Z","user":{"username":"bob"}}]`), nil
		default:
			return []byte(`{}`), nil
		}
	}}
	g := &GitLab{runner: runner, repo: "g/p", project: "g%2Fp"}
	items, err := g.List(context.Background(), CIRuns, Filter{Value: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "7" || items[0].Title != "validate" || items[0].State != "success" || items[0].Author != "bob" {
		t.Fatalf("unexpected GitLab pipeline: %#v", items)
	}
	if command := strings.Join(runner.calls[0], " "); !strings.Contains(command, "projects/g%2Fp/pipelines?per_page=100&order_by=updated_at&sort=desc&status=success") {
		t.Fatalf("unexpected GitLab pipeline list command: %s", command)
	}
	detail, err := g.Detail(context.Background(), CIRuns, items[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Sections) != 2 || detail.Sections[0].Title != "Logs · verify / test · success" || detail.Sections[0].Markdown != "    go test ./...\n    ok\n    " || detail.Sections[1].Markdown != "_No log output yet._" {
		t.Fatalf("unexpected GitLab pipeline logs: %#v", detail.Sections)
	}
	if err := g.CancelRun(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	if err := g.Rerun(context.Background(), items[0]); err != nil {
		t.Fatal(err)
	}
	cancel := strings.Join(runner.calls[len(runner.calls)-2], " ")
	retry := strings.Join(runner.calls[len(runner.calls)-1], " ")
	if !strings.Contains(cancel, "projects/g%2Fp/pipelines/7/cancel --hostname gitlab.com --method POST") || !strings.Contains(retry, "projects/g%2Fp/pipelines/7/retry --hostname gitlab.com --method POST") {
		t.Fatalf("unexpected GitLab pipeline actions:\n%s\n%s", cancel, retry)
	}
}

func TestGitHubCIRunDetailKeepsMetadataWhenLogsUnavailable(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "/jobs"):
			return nil, errors.New("jobs expired")
		case strings.Contains(command, "/logs"):
			return nil, errors.New("logs expired")
		default:
			return []byte(`{"id":42,"name":"CI","status":"completed","conclusion":"failure","head_branch":"main","run_attempt":1}`), nil
		}
	}}
	detail, err := (&GitHub{runner: runner, repo: "o/r"}).Detail(context.Background(), CIRuns, Item{ID: "42"})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Item.State != "completed/failure" || len(detail.Sections) != 2 || detail.Sections[0].Markdown != "_Job summary unavailable._" || detail.Sections[1].Markdown != "_Logs unavailable._" {
		t.Fatalf("unavailable logs hid run detail: %#v", detail)
	}
}

func TestGitLabDetailAttachesPositionedDiscussionNotesToDiff(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, "merge_requests/34/diffs?per_page=100&unidiff=true"):
			return []byte(`[{"old_path":"main.go","new_path":"main.go","diff":"@@ -8 +8 @@\n-old\n+new"}]`), nil
		case strings.Contains(command, "merge_requests/34/versions?per_page=1"):
			return []byte(`[{"base_commit_sha":"base","start_commit_sha":"start","head_commit_sha":"head"}]`), nil
		case strings.Contains(command, "merge_requests/34/discussions"):
			return []byte(`[
				{"id":"positioned","notes":[
					{"id":41,"body":"root","created_at":"2026-07-20T10:00:00Z","author":{"username":"alice"},"resolvable":true,"resolved":false,"position":{"position_type":"text","old_path":"main.go","new_path":"main.go","new_line":8}},
					{"id":42,"body":"reply","created_at":"2026-07-20T11:00:00Z","author":{"username":"bob"}}
				]},
				{"id":"general","notes":[{"body":"general","created_at":"2026-07-20T12:00:00Z","author":{"username":"carol"}}]}
			]`), nil
		case strings.Contains(command, "api user "):
			return []byte(`{"id":7,"username":"me"}`), nil
		default:
			return []byte(`{"iid":34,"title":"change","state":"opened","author":{"username":"author"}}`), nil
		}
	}}
	g := &GitLab{runner: runner, repo: "group/project", project: "group%2Fproject"}

	detail, err := g.Detail(context.Background(), PullRequests, Item{ID: "34"})
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffReview{
		{ID: "41", ThreadID: "positioned", Author: "alice", Body: "root", CreatedAt: parseTime("2026-07-20T10:00:00Z"), NewLine: 8, Side: ReviewSideNew, Resolvable: true, Replyable: true},
		{ID: "42", ThreadID: "positioned", Author: "bob", Body: "reply", CreatedAt: parseTime("2026-07-20T11:00:00Z"), NewLine: 8, Side: ReviewSideNew, Resolvable: true, Replyable: true},
	}
	if len(detail.Diffs) != 1 || !reflect.DeepEqual(detail.Diffs[0].Reviews, want) {
		t.Fatalf("attached GitLab reviews = %#v, want %#v", detail.Diffs, want)
	}
	foundGeneral := false
	for _, section := range detail.Sections {
		foundGeneral = foundGeneral || section.Markdown == "general"
	}
	if !foundGeneral {
		t.Fatal("unpositioned GitLab discussion was not preserved as a detail section")
	}
}

func TestGitLabDiscussionsCollectAllPages(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		count := 1
		if strings.Contains(command, "&page=1") {
			count = 100
		} else if !strings.Contains(command, "&page=2") {
			t.Fatalf("unexpected GitLab discussions request: %s", command)
		}
		discussions := make([]gitlabDiscussion, count)
		for index := range discussions {
			discussions[index].Notes = []gitlabNote{{Body: "general", Author: gitlabUser{Username: "alice"}}}
		}
		return json.Marshal(discussions)
	}}
	g := &GitLab{runner: runner, project: "group%2Fproject"}
	sections, reviews, err := g.discussions(context.Background(), "merge_requests", "34")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 101 || len(reviews) != 0 || len(runner.calls) != 2 {
		t.Fatalf("paginated GitLab discussions: sections=%d reviews=%d calls=%d", len(sections), len(reviews), len(runner.calls))
	}
}

func TestGitLabReviewThreadsSupportRepliesAndResolve(t *testing.T) {
	runner := &fakeRunner{}
	g := &GitLab{runner: runner, project: "group%2Fproject"}
	target := ReviewThreadTarget{ThreadID: "discussion/with slash"}
	if err := g.AddReviewReply(context.Background(), Item{ID: "34"}, target, "follow up"); err != nil {
		t.Fatal(err)
	}
	if err := g.ResolveReview(context.Background(), Item{ID: "34"}, target); err != nil {
		t.Fatal(err)
	}
	replyCommand := strings.Join(runner.calls[0], " ")
	resolveCommand := strings.Join(runner.calls[1], " ")
	for _, want := range []string{"merge_requests/34/discussions/discussion%2Fwith%20slash/notes", "--method POST", "body=follow up"} {
		if !strings.Contains(replyCommand, want) {
			t.Fatalf("GitLab reply omitted %q: %s", want, replyCommand)
		}
	}
	for _, want := range []string{"merge_requests/34/discussions/discussion%2Fwith%20slash", "--method PUT", "resolved=true"} {
		if !strings.Contains(resolveCommand, want) {
			t.Fatalf("GitLab resolve omitted %q: %s", want, resolveCommand)
		}
	}
}

func TestProvidersCreateTopLevelReviews(t *testing.T) {
	githubRunner := &fakeRunner{}
	github := &GitHub{runner: githubRunner, repo: "owner/repo"}
	if err := github.AddReview(context.Background(), Item{ID: "12"}, "overall"); err != nil {
		t.Fatal(err)
	}
	githubCommand := strings.Join(githubRunner.calls[0], " ")
	for _, part := range []string{"--method POST repos/owner/repo/pulls/12/reviews", "-f body=overall", "-f event=COMMENT"} {
		if !strings.Contains(githubCommand, part) {
			t.Fatalf("GitHub top-level review omitted %q: %s", part, githubCommand)
		}
	}

	gitlabRunner := &fakeRunner{}
	gitlab := &GitLab{runner: gitlabRunner, project: "group%2Fproject"}
	if err := gitlab.AddReview(context.Background(), Item{ID: "34"}, "overall"); err != nil {
		t.Fatal(err)
	}
	gitlabCommand := strings.Join(gitlabRunner.calls[0], " ")
	for _, part := range []string{"api projects/group%2Fproject/merge_requests/34/notes", "--method POST", "-f body=overall"} {
		if !strings.Contains(gitlabCommand, part) {
			t.Fatalf("GitLab top-level review omitted %q: %s", part, gitlabCommand)
		}
	}

	if err := github.AddReview(context.Background(), Item{ID: "12"}, "  "); err == nil {
		t.Fatal("GitHub accepted an empty top-level review")
	}
	if err := gitlab.AddReview(context.Background(), Item{}, "review"); err == nil {
		t.Fatal("GitLab accepted a top-level review without an item ID")
	}
	if len(githubRunner.calls) != 1 || len(gitlabRunner.calls) != 1 {
		t.Fatal("invalid top-level reviews issued API calls")
	}
}
