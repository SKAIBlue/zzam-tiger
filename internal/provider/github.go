package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type GitHub struct {
	runner Runner
	repo   string
	host   string
}

func NewGitHub(runner Runner, repo, host string) (*GitHub, error) {
	if host == "" {
		host = "github.com"
	}
	if _, err := runner.Run(context.Background(), "gh", "auth", "status", "--active", "--hostname", host); err != nil {
		return nil, &AuthError{Provider: "GitHub", Command: "gh auth login", Cause: err}
	}
	if repo == "" {
		out, err := runner.Run(context.Background(), "gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		if err != nil {
			return nil, fmt.Errorf("resolve GitHub repository: %w", err)
		}
		repo = strings.TrimSpace(string(out))
	}
	if len(strings.Split(repo, "/")) != 2 {
		return nil, fmt.Errorf("GitHub repository must be owner/name, got %q", repo)
	}
	qualifiedRepo := repo
	if host != "github.com" {
		qualifiedRepo = host + "/" + repo
	}
	if _, err := runner.Run(context.Background(), "gh", "repo", "view", qualifiedRepo, "--json", "nameWithOwner", "--jq", ".nameWithOwner"); err != nil {
		return nil, fmt.Errorf("open GitHub repository %q: %w", repo, err)
	}
	return &GitHub{runner: runner, repo: repo, host: host}, nil
}

func (g *GitHub) Name() string       { return "GitHub" }
func (g *GitHub) Repository() string { return g.repo }

func (g *GitHub) TabName(kind Kind) string { return kind.String() }

func (g *GitHub) Filters(kind Kind) []Filter {
	switch kind {
	case PullRequests:
		return []Filter{{"Open", "open"}, {"Closed", "closed"}, {"Merged", "merged"}, {"All", "all"}}
	case Issues, Milestones:
		return []Filter{{"Open", "open"}, {"Closed", "closed"}, {"All", "all"}}
	default:
		return []Filter{{"All", "all"}}
	}
}

func (g *GitHub) api(ctx context.Context, method, endpoint string, fields ...string) ([]byte, error) {
	host := g.host
	if host == "" {
		host = "github.com"
	}
	args := []string{"api", "--hostname", host, "--method", method, endpoint}
	for _, field := range fields {
		args = append(args, "-f", field)
	}
	return g.runner.Run(ctx, "gh", args...)
}

type githubUser struct {
	Login string `json:"login"`
}

type githubLabel struct {
	Name string `json:"name"`
}

type githubItem struct {
	Number      int           `json:"number"`
	Title       string        `json:"title"`
	State       string        `json:"state"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	UpdatedAt   string        `json:"updated_at"`
	CreatedAt   string        `json:"created_at"`
	MergedAt    *string       `json:"merged_at"`
	User        githubUser    `json:"user"`
	Labels      []githubLabel `json:"labels"`
	Comments    int           `json:"comments"`
	CommitCount int           `json:"commits"`
	Changed     int           `json:"changed_files"`
	Additions   int           `json:"additions"`
	Deletions   int           `json:"deletions"`
	Head        struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func githubListItem(raw githubItem) Item {
	state := strings.ToLower(raw.State)
	if raw.MergedAt != nil {
		state = "merged"
	}
	return Item{
		ID:        strconv.Itoa(raw.Number),
		Title:     raw.Title,
		State:     state,
		Author:    raw.User.Login,
		HeadSHA:   raw.Head.SHA,
		UpdatedAt: parseTime(raw.UpdatedAt),
		Meta:      fmt.Sprintf("#%d · %s", raw.Number, raw.User.Login),
		URL:       raw.HTMLURL,
	}
}

func (g *GitHub) List(ctx context.Context, kind Kind, filter Filter) ([]Item, error) {
	switch kind {
	case PullRequests:
		state := filter.Value
		if state == "merged" {
			state = "closed"
		}
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/pulls", "state="+state, "per_page=100", "sort=updated", "direction=desc")
		if err != nil {
			return nil, err
		}
		var raw []githubItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub pull requests: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			merged := entry.MergedAt != nil
			if filter.Value == "merged" && !merged || filter.Value == "closed" && merged {
				continue
			}
			items = append(items, githubListItem(entry))
		}
		return items, nil

	case Issues:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/issues", "state="+filter.Value, "per_page=100", "sort=updated", "direction=desc")
		if err != nil {
			return nil, err
		}
		var raw []githubItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub issues: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			if len(entry.PullRequest) > 0 && string(entry.PullRequest) != "null" {
				continue
			}
			items = append(items, githubListItem(entry))
		}
		return items, nil

	case Milestones:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/milestones", "state="+filter.Value, "per_page=100", "sort=due_on", "direction=asc")
		if err != nil {
			return nil, err
		}
		var raw []struct {
			Number       int        `json:"number"`
			Title        string     `json:"title"`
			State        string     `json:"state"`
			Description  string     `json:"description"`
			HTMLURL      string     `json:"html_url"`
			UpdatedAt    string     `json:"updated_at"`
			Creator      githubUser `json:"creator"`
			OpenIssues   int        `json:"open_issues"`
			ClosedIssues int        `json:"closed_issues"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub milestones: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			items = append(items, Item{ID: strconv.Itoa(entry.Number), Title: entry.Title, State: entry.State, Author: entry.Creator.Login, UpdatedAt: parseTime(entry.UpdatedAt), Meta: fmt.Sprintf("#%d · %d open / %d closed", entry.Number, entry.OpenIssues, entry.ClosedIssues), URL: entry.HTMLURL})
		}
		return items, nil

	case Branches:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/branches", "per_page=100")
		if err != nil {
			return nil, err
		}
		var raw []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
			Commit    struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub branches: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			protection := ""
			if entry.Protected {
				protection = " · protected"
			}
			items = append(items, Item{ID: entry.Name, Title: entry.Name, State: "branch", Meta: shortSHA(entry.Commit.SHA) + protection, URL: "https://github.com/" + g.repo + "/tree/" + url.PathEscape(entry.Name)})
		}
		return items, nil

	case Commits:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/commits", "per_page=100")
		if err != nil {
			return nil, err
		}
		var raw []githubCommit
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub commits: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			items = append(items, entry.item())
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported GitHub list kind: %s", kind)
	}
}

type githubComment struct {
	Body      string     `json:"body"`
	User      githubUser `json:"user"`
	CreatedAt string     `json:"created_at"`
	Submitted string     `json:"submitted_at"`
	State     string     `json:"state"`
}

type githubCommit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Stats struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Total     int `json:"total"`
	} `json:"stats"`
	Files []struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
		Changes  int    `json:"changes"`
	} `json:"files"`
}

func (c githubCommit) item() Item {
	title := strings.SplitN(c.Commit.Message, "\n", 2)[0]
	return Item{ID: c.SHA, Title: title, State: "commit", Author: c.Commit.Author.Name, UpdatedAt: parseTime(c.Commit.Author.Date), Meta: shortSHA(c.SHA) + " · " + c.Commit.Author.Name, URL: c.HTMLURL}
}

func (g *GitHub) Detail(ctx context.Context, kind Kind, item Item) (Detail, error) {
	if err := requireItem(kind, item); err != nil {
		return Detail{}, err
	}
	switch kind {
	case PullRequests, Issues:
		resource := "issues"
		if kind == PullRequests {
			resource = "pulls"
		}
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/"+resource+"/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw githubItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitHub detail: %w", err)
		}
		detail := Detail{Item: githubListItem(raw), Body: raw.Body}
		for _, label := range raw.Labels {
			detail.Labels = append(detail.Labels, label.Name)
		}
		if kind == PullRequests {
			detail.Sections = append(detail.Sections, Section{Title: "Changes", Markdown: fmt.Sprintf("`%s` → `%s` · **%d** commits · **%d** files · +%d / -%d", raw.Head.Ref, raw.Base.Ref, raw.CommitCount, raw.Changed, raw.Additions, raw.Deletions)})
		}
		comments, err := g.githubComments(ctx, item.ID, kind == PullRequests)
		if err != nil {
			return Detail{}, err
		}
		detail.Sections = append(detail.Sections, comments...)
		if kind == PullRequests {
			inline, inlineErr := g.githubReviewComments(ctx, item.ID)
			if inlineErr != nil {
				return Detail{}, inlineErr
			}
			detail.Sections = append(detail.Sections, inline...)
		}
		updates, err := g.githubUpdates(ctx, item.ID)
		if err != nil {
			return Detail{}, err
		}
		detail.Sections = append(detail.Sections, updates...)
		return detail, nil

	case Milestones:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/milestones/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw struct {
			Title        string     `json:"title"`
			Description  string     `json:"description"`
			State        string     `json:"state"`
			HTMLURL      string     `json:"html_url"`
			UpdatedAt    string     `json:"updated_at"`
			Creator      githubUser `json:"creator"`
			OpenIssues   int        `json:"open_issues"`
			ClosedIssues int        `json:"closed_issues"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitHub milestone: %w", err)
		}
		current := Item{ID: item.ID, Title: raw.Title, State: raw.State, Author: raw.Creator.Login, UpdatedAt: parseTime(raw.UpdatedAt), Meta: fmt.Sprintf("%d open / %d closed", raw.OpenIssues, raw.ClosedIssues), URL: raw.HTMLURL}
		return Detail{Item: current, Body: raw.Description}, nil

	case Branches:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/branches/"+url.PathEscape(item.ID))
		if err != nil {
			return Detail{}, err
		}
		var raw struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
			Commit    struct {
				SHA    string `json:"sha"`
				Commit struct {
					Message string `json:"message"`
					Author  struct {
						Name string `json:"name"`
						Date string `json:"date"`
					} `json:"author"`
				} `json:"commit"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitHub branch: %w", err)
		}
		body := fmt.Sprintf("- Latest commit: `%s`\n- Protected: **%t**\n- Author: %s\n\n%s", raw.Commit.SHA, raw.Protected, raw.Commit.Commit.Author.Name, raw.Commit.Commit.Message)
		current := item
		current.Meta = shortSHA(raw.Commit.SHA)
		return Detail{Item: current, Body: body}, nil

	case Commits:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/commits/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw githubCommit
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitHub commit: %w", err)
		}
		files := make([]string, 0, len(raw.Files))
		for _, file := range raw.Files {
			files = append(files, fmt.Sprintf("- `%s` · %s · %d changes", file.Filename, file.Status, file.Changes))
		}
		return Detail{Item: raw.item(), Body: raw.Commit.Message, Sections: []Section{{Title: "Stats", Markdown: fmt.Sprintf("**%d** files changed · +%d / -%d", len(raw.Files), raw.Stats.Additions, raw.Stats.Deletions)}, {Title: "Files", Markdown: strings.Join(files, "\n")}}}, nil
	default:
		return Detail{}, fmt.Errorf("unsupported GitHub detail kind: %s", kind)
	}
}

func (g *GitHub) githubComments(ctx context.Context, id string, includeReviews bool) ([]Section, error) {
	data, err := g.api(ctx, "GET", "repos/"+g.repo+"/issues/"+id+"/comments", "per_page=100")
	if err != nil {
		return nil, err
	}
	var comments []githubComment
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, fmt.Errorf("decode GitHub comments: %w", err)
	}
	sections := make([]Section, 0, len(comments))
	for _, comment := range comments {
		sections = append(sections, Section{Title: "Comment · @" + comment.User.Login + " · " + displayTime(parseTime(comment.CreatedAt)), Markdown: comment.Body})
	}
	if !includeReviews {
		return sections, nil
	}
	data, err = g.api(ctx, "GET", "repos/"+g.repo+"/pulls/"+id+"/reviews", "per_page=100")
	if err != nil {
		return nil, err
	}
	var reviews []githubComment
	if err := json.Unmarshal(data, &reviews); err != nil {
		return nil, fmt.Errorf("decode GitHub reviews: %w", err)
	}
	for _, review := range reviews {
		body := review.Body
		if body == "" {
			body = "_No review body._"
		}
		timestamp := review.Submitted
		if timestamp == "" {
			timestamp = review.CreatedAt
		}
		sections = append(sections, Section{Title: "Review · @" + review.User.Login + " · " + strings.ToLower(review.State) + " · " + displayTime(parseTime(timestamp)), Markdown: body})
	}
	return sections, nil
}

func (g *GitHub) githubReviewComments(ctx context.Context, id string) ([]Section, error) {
	data, err := g.api(ctx, "GET", "repos/"+g.repo+"/pulls/"+id+"/comments", "per_page=100")
	if err != nil {
		return nil, err
	}
	var comments []struct {
		Body      string     `json:"body"`
		User      githubUser `json:"user"`
		CreatedAt string     `json:"created_at"`
		Path      string     `json:"path"`
		Line      *int       `json:"line"`
	}
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, fmt.Errorf("decode GitHub review comments: %w", err)
	}
	sections := make([]Section, 0, len(comments))
	for _, comment := range comments {
		location := "`" + comment.Path + "`"
		if comment.Line != nil {
			location += fmt.Sprintf(":%d", *comment.Line)
		}
		sections = append(sections, Section{Title: "Inline comment · @" + comment.User.Login + " · " + location + " · " + displayTime(parseTime(comment.CreatedAt)), Markdown: comment.Body})
	}
	return sections, nil
}

func (g *GitHub) githubUpdates(ctx context.Context, id string) ([]Section, error) {
	data, err := g.api(ctx, "GET", "repos/"+g.repo+"/issues/"+id+"/timeline", "per_page=100")
	if err != nil {
		return nil, err
	}
	var events []struct {
		Event     string      `json:"event"`
		CreatedAt string      `json:"created_at"`
		Actor     githubUser  `json:"actor"`
		Label     githubLabel `json:"label"`
		Rename    struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"rename"`
		CommitID string `json:"commit_id"`
	}
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode GitHub timeline: %w", err)
	}
	sections := make([]Section, 0, len(events))
	for _, event := range events {
		var body string
		switch event.Event {
		case "closed", "reopened", "merged", "assigned", "unassigned", "locked", "unlocked", "milestoned", "demilestoned", "marked_as_duplicate", "unmarked_as_duplicate", "converted_to_draft", "ready_for_review":
			body = fmt.Sprintf("@%s **%s** this item.", event.Actor.Login, strings.ReplaceAll(event.Event, "_", " "))
		case "labeled", "unlabeled":
			body = fmt.Sprintf("@%s **%s** `%s`.", event.Actor.Login, event.Event, event.Label.Name)
		case "renamed":
			body = fmt.Sprintf("@%s renamed `%s` to `%s`.", event.Actor.Login, event.Rename.From, event.Rename.To)
		case "committed":
			body = fmt.Sprintf("@%s added commit `%s`.", event.Actor.Login, shortSHA(event.CommitID))
		default:
			continue
		}
		sections = append(sections, Section{Title: "Update · " + displayTime(parseTime(event.CreatedAt)), Markdown: body})
	}
	return sections, nil
}

func (g *GitHub) Merge(ctx context.Context, item Item) error {
	if item.HeadSHA == "" {
		return fmt.Errorf("refusing to merge pull request %s without a head SHA", item.ID)
	}
	fields := []string{"merge_method=merge", "sha=" + item.HeadSHA}
	_, err := g.api(ctx, "PUT", "repos/"+g.repo+"/pulls/"+item.ID+"/merge", fields...)
	return err
}

func (g *GitHub) SetIssueState(ctx context.Context, item Item, open bool) error {
	state := "closed"
	if open {
		state = "open"
	}
	_, err := g.api(ctx, "PATCH", "repos/"+g.repo+"/issues/"+item.ID, "state="+state)
	return err
}

func (g *GitHub) SetIssueLabels(ctx context.Context, item Item, labels []string) error {
	if len(labels) == 0 {
		_, err := g.api(ctx, "DELETE", "repos/"+g.repo+"/issues/"+item.ID+"/labels")
		return err
	}
	args := make([]string, 0, len(labels))
	for _, label := range labels {
		args = append(args, "labels[]="+label)
	}
	_, err := g.api(ctx, "PATCH", "repos/"+g.repo+"/issues/"+item.ID, args...)
	return err
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func displayTime(value interface{ Format(string) string }) string {
	return value.Format("2006-01-02 15:04")
}
