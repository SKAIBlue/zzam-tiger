package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type GitLab struct {
	runner  Runner
	repo    string
	project string
	host    string

	userMu sync.Mutex
	user   *Assignee
}

func NewGitLab(runner Runner, repo, host string) (*GitLab, error) {
	if host == "" {
		host = "gitlab.com"
	}
	if _, err := runner.Run(context.Background(), "glab", "auth", "status", "--hostname", host); err != nil {
		return nil, &AuthError{Provider: "GitLab", Command: "glab auth login", Cause: err}
	}
	if repo == "" {
		out, err := runner.Run(context.Background(), "git", "remote", "get-url", "origin")
		if err != nil {
			return nil, fmt.Errorf("resolve GitLab repository: %w", err)
		}
		_, parsed, ok := ParseRepositoryURL(string(out))
		if !ok {
			return nil, fmt.Errorf("resolve GitLab repository from origin")
		}
		repo = parsed
	}
	if !strings.Contains(repo, "/") {
		return nil, fmt.Errorf("GitLab repository must be group/project, got %q", repo)
	}
	if _, err := runner.Run(context.Background(), "glab", "repo", "view", "https://"+host+"/"+repo, "--output", "json"); err != nil {
		return nil, fmt.Errorf("open GitLab repository %q: %w", repo, err)
	}
	return &GitLab{runner: runner, repo: repo, project: url.PathEscape(repo), host: host}, nil
}

func (g *GitLab) Name() string       { return "GitLab" }
func (g *GitLab) Repository() string { return g.repo }

func (g *GitLab) TabName(kind Kind) string {
	if kind == PullRequests {
		return "Merge Requests"
	}
	return kind.String()
}

func (g *GitLab) Filters(kind Kind) []Filter {
	switch kind {
	case PullRequests:
		return []Filter{{"Open", "opened"}, {"Assigned to me", "assigned"}, {"Closed", "closed"}, {"Merged", "merged"}, {"All", "all"}}
	case Issues:
		return []Filter{{"Open", "opened"}, {"Assigned to me", "assigned"}, {"Closed", "closed"}, {"All", "all"}}
	case Milestones:
		return []Filter{{"Open", "active"}, {"Closed", "closed"}, {"All", "all"}}
	default:
		return []Filter{{"All", "all"}}
	}
}

func (g *GitLab) api(ctx context.Context, method, endpoint string, fields ...string) ([]byte, error) {
	host := g.host
	if host == "" {
		host = "gitlab.com"
	}
	args := []string{"api", endpoint, "--hostname", host, "--method", method}
	for _, field := range fields {
		args = append(args, "-f", field)
	}
	return g.runner.Run(ctx, "glab", args...)
}

type gitlabUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type gitlabItem struct {
	IID            int          `json:"iid"`
	ID             int          `json:"id"`
	Title          string       `json:"title"`
	State          string       `json:"state"`
	Description    string       `json:"description"`
	WebURL         string       `json:"web_url"`
	UpdatedAt      string       `json:"updated_at"`
	CreatedAt      string       `json:"created_at"`
	MergedAt       *string      `json:"merged_at"`
	Author         gitlabUser   `json:"author"`
	Assignees      []gitlabUser `json:"assignees"`
	Labels         []string     `json:"labels"`
	SourceBranch   string       `json:"source_branch"`
	TargetBranch   string       `json:"target_branch"`
	UserNotesCount int          `json:"user_notes_count"`
	ChangesCount   string       `json:"changes_count"`
	DetailedStatus string       `json:"detailed_merge_status"`
	SHA            string       `json:"sha"`
	DueDate        string       `json:"due_date"`
	Stats          struct {
		Total  int `json:"total"`
		Closed int `json:"closed"`
	} `json:"stats"`
}

func gitlabListItem(raw gitlabItem) Item {
	state := raw.State
	if raw.MergedAt != nil {
		state = "merged"
	}
	author := raw.Author.Username
	if author == "" {
		author = raw.Author.Name
	}
	item := Item{ID: strconv.Itoa(raw.IID), Title: raw.Title, State: state, Author: author, HeadSHA: raw.SHA, UpdatedAt: parseTime(raw.UpdatedAt), Meta: fmt.Sprintf("!%d · %s", raw.IID, author), URL: raw.WebURL}
	for _, assignee := range raw.Assignees {
		login := assignee.Username
		if login == "" {
			login = assignee.Name
		}
		item.Assignees = append(item.Assignees, Assignee{ID: strconv.Itoa(assignee.ID), Login: login})
	}
	return item
}

func (g *GitLab) List(ctx context.Context, kind Kind, filter Filter) ([]Item, error) {
	base := "projects/" + g.project
	switch kind {
	case PullRequests, Issues, Milestones:
		resource := map[Kind]string{PullRequests: "merge_requests", Issues: "issues", Milestones: "milestones"}[kind]
		endpoint := base + "/" + resource + "?per_page=100"
		if kind != Milestones {
			endpoint += "&order_by=updated_at&sort=desc"
		}
		if kind != Milestones {
			scope := "all"
			if filter.Value == "assigned" {
				scope = "assigned_to_me"
			}
			endpoint += "&scope=" + scope
		}
		if filter.Value != "all" && filter.Value != "assigned" {
			endpoint += "&state=" + url.QueryEscape(filter.Value)
		} else if filter.Value == "assigned" {
			endpoint += "&state=opened"
		}
		data, err := g.api(ctx, "GET", endpoint)
		if err != nil {
			return nil, err
		}
		var raw []gitlabItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitLab %s: %w", resource, err)
		}
		var viewer Assignee
		if kind == PullRequests || kind == Issues {
			viewer, err = g.currentUser(ctx)
			if err != nil {
				return nil, err
			}
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			current := gitlabListItem(entry)
			if kind == PullRequests || kind == Issues {
				current.AssignedToMe = hasAssignee(current.Assignees, viewer)
			}
			if kind == Issues {
				current.Meta = fmt.Sprintf("#%d · %s", entry.IID, current.Author)
			} else if kind == Milestones {
				current.ID = strconv.Itoa(entry.ID)
				current.Meta = fmt.Sprintf("%d open / %d closed", entry.Stats.Total-entry.Stats.Closed, entry.Stats.Closed)
			}
			items = append(items, current)
		}
		return items, nil

	case Branches:
		data, err := g.api(ctx, "GET", base+"/repository/branches?per_page=100")
		if err != nil {
			return nil, err
		}
		var raw []struct {
			Name      string `json:"name"`
			Merged    bool   `json:"merged"`
			Protected bool   `json:"protected"`
			Default   bool   `json:"default"`
			WebURL    string `json:"web_url"`
			Commit    struct {
				ID            string `json:"id"`
				Title         string `json:"title"`
				AuthorName    string `json:"author_name"`
				CommittedDate string `json:"committed_date"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitLab branches: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			badges := ""
			if entry.Default {
				badges += " · default"
			}
			if entry.Protected {
				badges += " · protected"
			}
			items = append(items, Item{ID: entry.Name, Title: entry.Name, State: "branch", Author: entry.Commit.AuthorName, UpdatedAt: parseTime(entry.Commit.CommittedDate), Meta: shortSHA(entry.Commit.ID) + badges, URL: entry.WebURL})
		}
		return items, nil

	case Commits:
		data, err := g.api(ctx, "GET", base+"/repository/commits?per_page=100")
		if err != nil {
			return nil, err
		}
		var raw []gitlabCommit
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitLab commits: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			items = append(items, entry.item())
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported GitLab list kind: %s", kind)
	}
}

type gitlabCommit struct {
	ID            string `json:"id"`
	ShortID       string `json:"short_id"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	AuthorName    string `json:"author_name"`
	CommittedDate string `json:"committed_date"`
	WebURL        string `json:"web_url"`
	Stats         struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Total     int `json:"total"`
	} `json:"stats"`
}

func (c gitlabCommit) item() Item {
	short := c.ShortID
	if short == "" {
		short = shortSHA(c.ID)
	}
	return Item{ID: c.ID, Title: c.Title, State: "commit", Author: c.AuthorName, UpdatedAt: parseTime(c.CommittedDate), Meta: short + " · " + c.AuthorName, URL: c.WebURL}
}

type gitlabNote struct {
	Body      string     `json:"body"`
	System    bool       `json:"system"`
	CreatedAt string     `json:"created_at"`
	Author    gitlabUser `json:"author"`
}

func (g *GitLab) Detail(ctx context.Context, kind Kind, item Item) (Detail, error) {
	if err := requireItem(kind, item); err != nil {
		return Detail{}, err
	}
	base := "projects/" + g.project
	switch kind {
	case PullRequests, Issues:
		resource := "issues"
		if kind == PullRequests {
			resource = "merge_requests"
		}
		data, err := g.api(ctx, "GET", base+"/"+resource+"/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw gitlabItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab detail: %w", err)
		}
		detail := Detail{Item: gitlabListItem(raw), Body: raw.Description, Labels: raw.Labels}
		current, err := g.currentUser(ctx)
		if err != nil {
			return Detail{}, err
		}
		detail.Item.AssignedToMe = hasAssignee(detail.Item.Assignees, current)
		if kind == Issues {
			detail.Item.Meta = fmt.Sprintf("#%d · %s", raw.IID, detail.Item.Author)
		} else {
			detail.Sections = append(detail.Sections, Section{Title: "Changes", Markdown: fmt.Sprintf("`%s` → `%s` · **%s** files changed · merge status: **%s**", raw.SourceBranch, raw.TargetBranch, raw.ChangesCount, raw.DetailedStatus)})
		}
		notes, err := g.discussions(ctx, resource, item.ID)
		if err != nil {
			return Detail{}, err
		}
		detail.Sections = append(detail.Sections, notes...)
		return detail, nil

	case Milestones:
		data, err := g.api(ctx, "GET", base+"/milestones/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw gitlabItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab milestone: %w", err)
		}
		current := Item{ID: item.ID, Title: raw.Title, State: raw.State, UpdatedAt: parseTime(raw.UpdatedAt), Meta: fmt.Sprintf("%d open / %d closed", raw.Stats.Total-raw.Stats.Closed, raw.Stats.Closed), URL: raw.WebURL}
		body := raw.Description
		if raw.DueDate != "" {
			body += "\n\nDue: **" + raw.DueDate + "**"
		}
		return Detail{Item: current, Body: body}, nil

	case Branches:
		data, err := g.api(ctx, "GET", base+"/repository/branches/"+url.PathEscape(item.ID))
		if err != nil {
			return Detail{}, err
		}
		var raw struct {
			Name      string       `json:"name"`
			Merged    bool         `json:"merged"`
			Protected bool         `json:"protected"`
			Default   bool         `json:"default"`
			WebURL    string       `json:"web_url"`
			Commit    gitlabCommit `json:"commit"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab branch: %w", err)
		}
		body := fmt.Sprintf("- Latest commit: `%s`\n- Default: **%t**\n- Protected: **%t**\n- Merged: **%t**\n- Author: %s\n\n%s", raw.Commit.ID, raw.Default, raw.Protected, raw.Merged, raw.Commit.AuthorName, raw.Commit.Message)
		current := item
		current.URL = raw.WebURL
		return Detail{Item: current, Body: body}, nil

	case Commits:
		data, err := g.api(ctx, "GET", base+"/repository/commits/"+item.ID+"?stats=true")
		if err != nil {
			return Detail{}, err
		}
		var raw gitlabCommit
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab commit: %w", err)
		}
		diffData, err := g.api(ctx, "GET", base+"/repository/commits/"+item.ID+"/diff?per_page=100")
		if err != nil {
			return Detail{}, err
		}
		var diffs []struct {
			NewPath     string `json:"new_path"`
			OldPath     string `json:"old_path"`
			NewFile     bool   `json:"new_file"`
			RenamedFile bool   `json:"renamed_file"`
			DeletedFile bool   `json:"deleted_file"`
		}
		if err := json.Unmarshal(diffData, &diffs); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab commit diff: %w", err)
		}
		files := make([]string, 0, len(diffs))
		for _, diff := range diffs {
			status := "modified"
			switch {
			case diff.NewFile:
				status = "added"
			case diff.DeletedFile:
				status = "deleted"
			case diff.RenamedFile:
				status = "renamed from `" + diff.OldPath + "`"
			}
			files = append(files, fmt.Sprintf("- `%s` · %s", diff.NewPath, status))
		}
		return Detail{Item: raw.item(), Body: raw.Message, Sections: []Section{{Title: "Stats", Markdown: fmt.Sprintf("**%d** files changed · +%d / -%d", raw.Stats.Total, raw.Stats.Additions, raw.Stats.Deletions)}, {Title: "Files", Markdown: strings.Join(files, "\n")}}}, nil
	default:
		return Detail{}, fmt.Errorf("unsupported GitLab detail kind: %s", kind)
	}
}

func (g *GitLab) discussions(ctx context.Context, resource, id string) ([]Section, error) {
	data, err := g.api(ctx, "GET", "projects/"+g.project+"/"+resource+"/"+id+"/discussions?per_page=100")
	if err != nil {
		return nil, err
	}
	var discussions []struct {
		ID    string       `json:"id"`
		Notes []gitlabNote `json:"notes"`
	}
	if err := json.Unmarshal(data, &discussions); err != nil {
		return nil, fmt.Errorf("decode GitLab discussions: %w", err)
	}
	sections := make([]Section, 0, len(discussions))
	for _, discussion := range discussions {
		for noteIndex, note := range discussion.Notes {
			kind := "Comment"
			if len(discussion.Notes) > 1 {
				kind = "Thread reply"
				if noteIndex == 0 {
					kind = "Thread"
				}
			}
			if note.System {
				kind = "Update"
			}
			author := note.Author.Username
			if author == "" {
				author = note.Author.Name
			}
			sections = append(sections, Section{Title: kind + " · @" + author + " · " + displayTime(parseTime(note.CreatedAt)), Markdown: note.Body})
		}
	}
	return sections, nil
}

func (g *GitLab) Merge(ctx context.Context, item Item) error {
	if item.HeadSHA == "" {
		return fmt.Errorf("refusing to merge merge request %s without a head SHA", item.ID)
	}
	fields := []string{"sha=" + item.HeadSHA}
	_, err := g.api(ctx, "PUT", "projects/"+g.project+"/merge_requests/"+item.ID+"/merge", fields...)
	return err
}

func (g *GitLab) SetIssueState(ctx context.Context, item Item, open bool) error {
	event := "close"
	if open {
		event = "reopen"
	}
	_, err := g.api(ctx, "PUT", "projects/"+g.project+"/issues/"+item.ID, "state_event="+event)
	return err
}

func (g *GitLab) SetAssigned(ctx context.Context, kind Kind, item Item, assigned bool) error {
	if err := requireAssignable(kind, item); err != nil {
		return err
	}
	current, err := g.currentUser(ctx)
	if err != nil {
		return err
	}
	mutation := "issueSetAssignees"
	if kind == PullRequests {
		mutation = "mergeRequestSetAssignees"
	}
	mode := "APPEND"
	if !assigned {
		mode = "REMOVE"
	}
	query := fmt.Sprintf(
		"mutation { assignment: %s(input: { projectPath: %s, iid: %s, assigneeUsernames: [%s], operationMode: %s }) { errors } }",
		mutation,
		strconv.Quote(g.repo),
		strconv.Quote(item.ID),
		strconv.Quote(current.Login),
		mode,
	)
	data, err := g.api(ctx, "POST", "graphql", "query="+query)
	if err != nil {
		return err
	}
	var response struct {
		Data struct {
			Assignment struct {
				Errors []string `json:"errors"`
			} `json:"assignment"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("decode GitLab assignment response: %w", err)
	}
	errors := append([]string(nil), response.Data.Assignment.Errors...)
	for _, graphQLError := range response.Errors {
		errors = append(errors, graphQLError.Message)
	}
	if len(errors) > 0 {
		return fmt.Errorf("update GitLab assignment: %s", strings.Join(errors, "; "))
	}
	return nil
}

func (g *GitLab) currentUser(ctx context.Context) (Assignee, error) {
	g.userMu.Lock()
	defer g.userMu.Unlock()
	if g.user != nil {
		return *g.user, nil
	}
	data, err := g.api(ctx, "GET", "user")
	if err != nil {
		return Assignee{}, fmt.Errorf("resolve GitLab user: %w", err)
	}
	var raw gitlabUser
	if err := json.Unmarshal(data, &raw); err != nil {
		return Assignee{}, fmt.Errorf("decode GitLab user: %w", err)
	}
	login := raw.Username
	if login == "" {
		login = raw.Name
	}
	if raw.ID == 0 || login == "" {
		return Assignee{}, fmt.Errorf("resolve GitLab user: response has no id or username")
	}
	current := Assignee{ID: strconv.Itoa(raw.ID), Login: login}
	g.user = &current
	return current, nil
}

func (g *GitLab) SetIssueLabels(ctx context.Context, item Item, labels []string) error {
	_, err := g.api(ctx, "PUT", "projects/"+g.project+"/issues/"+item.ID, "labels="+strings.Join(labels, ","))
	return err
}
