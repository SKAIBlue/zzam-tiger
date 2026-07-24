package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type GitHub struct {
	runner Runner
	repo   string
	host   string

	userMu sync.Mutex
	user   *Assignee
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
func (g *GitHub) cacheHost() string  { return g.host }

func (g *GitHub) TabName(kind Kind) string {
	if kind == CIRuns {
		return "Actions"
	}
	return kind.String()
}

func (g *GitHub) Filters(kind Kind) []Filter {
	switch kind {
	case PullRequests:
		return []Filter{{"Open", "open"}, {"Assigned to me", "assigned"}, {"Closed", "closed"}, {"Merged", "merged"}, {"All", "all"}}
	case Issues:
		return []Filter{{"Open", "open"}, {"Assigned to me", "assigned"}, {"Closed", "closed"}, {"All", "all"}}
	case Milestones:
		return []Filter{{"Open", "open"}, {"Closed", "closed"}, {"All", "all"}}
	case CIRuns:
		return []Filter{{"All", "all"}, {"In progress", "in_progress"}, {"Queued", "queued"}, {"Success", "success"}, {"Failure", "failure"}}
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
	ID    int    `json:"id"`
	Login string `json:"login"`
}

type githubLabel struct {
	Name string `json:"name"`
}

type githubPullRequestLink struct {
	MergedAt *string `json:"merged_at"`
}

type githubRun struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	DisplayTitle string     `json:"display_title"`
	Status       string     `json:"status"`
	Conclusion   string     `json:"conclusion"`
	Event        string     `json:"event"`
	HeadBranch   string     `json:"head_branch"`
	HeadSHA      string     `json:"head_sha"`
	RunAttempt   int        `json:"run_attempt"`
	CreatedAt    string     `json:"created_at"`
	UpdatedAt    string     `json:"updated_at"`
	HTMLURL      string     `json:"html_url"`
	Actor        githubUser `json:"actor"`
}

type githubJob struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Steps      []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"steps"`
}

func (r githubRun) item() Item {
	title := r.DisplayTitle
	if title == "" {
		title = r.Name
	}
	state := r.Status
	if r.Conclusion != "" {
		state += "/" + r.Conclusion
	}
	meta := r.Name
	if r.HeadBranch != "" {
		meta += " · " + r.HeadBranch
	}
	if r.Event != "" {
		meta += " · " + r.Event
	}
	if r.RunAttempt > 1 {
		meta += fmt.Sprintf(" · attempt %d", r.RunAttempt)
	}
	return Item{ID: strconv.FormatInt(r.ID, 10), Title: title, State: state, Author: r.Actor.Login, HeadSHA: r.HeadSHA, UpdatedAt: parseTime(r.UpdatedAt), Meta: meta, URL: r.HTMLURL}
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
	Assignees   []githubUser  `json:"assignees"`
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
		SHA string `json:"sha"`
	} `json:"base"`
	PullRequest *githubPullRequestLink `json:"pull_request"`
}

func githubListItem(raw githubItem) Item {
	state := strings.ToLower(raw.State)
	if raw.MergedAt != nil {
		state = "merged"
	}
	item := Item{
		ID:        strconv.Itoa(raw.Number),
		Title:     raw.Title,
		State:     state,
		Author:    raw.User.Login,
		HeadSHA:   raw.Head.SHA,
		UpdatedAt: parseTime(raw.UpdatedAt),
		Meta:      fmt.Sprintf("#%d · %s", raw.Number, raw.User.Login),
		URL:       raw.HTMLURL,
	}
	for _, assignee := range raw.Assignees {
		id := ""
		if assignee.ID > 0 {
			id = strconv.Itoa(assignee.ID)
		}
		item.Assignees = append(item.Assignees, Assignee{ID: id, Login: assignee.Login})
	}
	return item
}

func (g *GitHub) List(ctx context.Context, kind Kind, filter Filter) ([]Item, error) {
	switch kind {
	case PullRequests:
		current, err := g.currentUser(ctx)
		if err != nil {
			return nil, err
		}
		if filter.Value == "assigned" {
			return g.listAssigned(ctx, kind, current)
		}
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
			item := githubListItem(entry)
			item.AssignedToMe = hasAssignee(item.Assignees, current)
			items = append(items, item)
		}
		return items, nil

	case Issues:
		current, err := g.currentUser(ctx)
		if err != nil {
			return nil, err
		}
		if filter.Value == "assigned" {
			return g.listAssigned(ctx, kind, current)
		}
		fields := []string{"state=" + filter.Value, "per_page=100", "sort=updated", "direction=desc"}
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/issues", fields...)
		if err != nil {
			return nil, err
		}
		var raw []githubItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub issues: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, entry := range raw {
			if entry.PullRequest != nil {
				continue
			}
			item := githubListItem(entry)
			item.AssignedToMe = hasAssignee(item.Assignees, current)
			items = append(items, item)
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
	case CIRuns:
		fields := []string{"per_page=100"}
		if filter.Value != "" && filter.Value != "all" {
			fields = append(fields, "status="+filter.Value)
		}
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/actions/runs", fields...)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Runs []githubRun `json:"workflow_runs"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitHub Actions runs: %w", err)
		}
		items := make([]Item, 0, len(raw.Runs))
		for _, run := range raw.Runs {
			items = append(items, run.item())
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported GitHub list kind: %s", kind)
	}
}

func (g *GitHub) listAssigned(ctx context.Context, kind Kind, current Assignee) ([]Item, error) {
	typeQualifier := "is:issue"
	if kind == PullRequests {
		typeQualifier = "is:pr"
	}
	query := "repo:" + g.repo + " " + typeQualifier + " is:open assignee:" + current.Login
	data, err := g.api(ctx, "GET", "search/issues", "q="+query, "per_page=100", "sort=updated", "order=desc")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []githubItem `json:"items"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode assigned GitHub %s: %w", kind, err)
	}
	items := make([]Item, 0, len(raw.Items))
	for _, entry := range raw.Items {
		if entry.PullRequest != nil && entry.PullRequest.MergedAt != nil {
			entry.MergedAt = entry.PullRequest.MergedAt
		}
		item := githubListItem(entry)
		if !hasAssignee(item.Assignees, current) {
			item.Assignees = append(item.Assignees, current)
		}
		item.AssignedToMe = true
		items = append(items, item)
	}
	return items, nil
}

type githubComment struct {
	Body      string     `json:"body"`
	User      githubUser `json:"user"`
	CreatedAt string     `json:"created_at"`
	Submitted string     `json:"submitted_at"`
	State     string     `json:"state"`
}

type githubReviewComment struct {
	ID            int64      `json:"id"`
	InReplyToID   *int64     `json:"in_reply_to_id"`
	Body          string     `json:"body"`
	User          githubUser `json:"user"`
	CreatedAt     string     `json:"created_at"`
	Path          string     `json:"path"`
	Line          *int       `json:"line"`
	OriginalLine  *int       `json:"original_line"`
	StartLine     *int       `json:"start_line"`
	OriginalStart *int       `json:"original_start_line"`
	Side          string     `json:"side"`
	StartSide     string     `json:"start_side"`
	SubjectType   string     `json:"subject_type"`
}

type githubReviewThreadInfo struct {
	ID         string
	Resolved   bool
	Resolvable bool
	CanReply   bool
}

type githubCommitComment struct {
	Body      string     `json:"body"`
	User      githubUser `json:"user"`
	CreatedAt string     `json:"created_at"`
	Path      string     `json:"path"`
	Position  *int       `json:"position"`
	Line      *int       `json:"line"`
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
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
	Files []struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Status           string `json:"status"`
		Changes          int    `json:"changes"`
		Patch            string `json:"patch"`
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
		current, err := g.currentUser(ctx)
		if err != nil {
			return Detail{}, err
		}
		detail.Item.AssignedToMe = hasAssignee(detail.Item.Assignees, current)
		for _, label := range raw.Labels {
			detail.Labels = append(detail.Labels, label.Name)
		}
		if kind == PullRequests {
			detail.Sections = append(detail.Sections, Section{Title: "Changes", Markdown: fmt.Sprintf("`%s` → `%s` · **%d** commits · **%d** files · +%d / -%d", raw.Head.Ref, raw.Base.Ref, raw.CommitCount, raw.Changed, raw.Additions, raw.Deletions)})
			files, err := g.githubPullRequestDiffs(ctx, item.ID, raw.Base.SHA, raw.Head.SHA)
			if err != nil {
				return Detail{}, err
			}
			detail.Diffs = files
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
			detail.Sections = append(detail.Sections, attachDiffReviews(detail.Diffs, inline)...)
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
		diffs := make([]DiffFile, 0, len(raw.Files))
		baseSHA := ""
		if len(raw.Parents) > 0 {
			baseSHA = raw.Parents[0].SHA
		}
		for _, file := range raw.Files {
			files = append(files, fmt.Sprintf("- `%s` · %s · %d changes", file.Filename, file.Status, file.Changes))
			oldPath := file.PreviousFilename
			if oldPath == "" {
				oldPath = file.Filename
			}
			diffs = append(diffs, DiffFile{
				OldPath:  oldPath,
				NewPath:  file.Filename,
				Lines:    ParseUnifiedDiffLines(file.Patch),
				TooLarge: file.Patch == "",
				BaseSHA:  baseSHA,
				HeadSHA:  raw.SHA,
			})
		}
		detail := Detail{Item: raw.item(), Body: raw.Commit.Message, Sections: []Section{{Title: "Stats", Markdown: fmt.Sprintf("**%d** files changed · +%d / -%d", len(raw.Files), raw.Stats.Additions, raw.Stats.Deletions)}, {Title: "Files", Markdown: strings.Join(files, "\n")}}, Diffs: diffs}
		comments, err := g.githubCommitComments(ctx, item.ID)
		if err != nil {
			return Detail{}, err
		}
		detail.Sections = append(detail.Sections, attachDiffReviews(detail.Diffs, githubCommitPositionedComments(detail.Diffs, comments))...)
		return detail, nil
	case CIRuns:
		data, err := g.api(ctx, "GET", "repos/"+g.repo+"/actions/runs/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw githubRun
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitHub Actions run: %w", err)
		}
		detail := Detail{
			Item: raw.item(),
			Body: fmt.Sprintf("- Workflow: **%s**\n- Event: `%s`\n- Branch: `%s`\n- Commit: `%s`\n- Attempt: **%d**", raw.Name, raw.Event, raw.HeadBranch, raw.HeadSHA, raw.RunAttempt),
		}
		jobData, jobErr := g.api(ctx, "GET", "repos/"+g.repo+"/actions/runs/"+item.ID+"/jobs", "per_page=100")
		if jobErr != nil {
			detail.Sections = append(detail.Sections, Section{Title: "Jobs", Markdown: "_Job summary unavailable._"})
		} else {
			var response struct {
				Jobs []githubJob `json:"jobs"`
			}
			if err := json.Unmarshal(jobData, &response); err != nil {
				detail.Sections = append(detail.Sections, Section{Title: "Jobs", Markdown: "_Job summary unavailable._"})
			} else {
				var summaries []string
				for _, job := range response.Jobs {
					state := job.Status
					if job.Conclusion != "" {
						state += "/" + job.Conclusion
					}
					summaries = append(summaries, "- **"+job.Name+"** · "+state)
					for _, step := range job.Steps {
						stepState := step.Status
						if step.Conclusion != "" {
							stepState += "/" + step.Conclusion
						}
						summaries = append(summaries, "  - "+step.Name+" · "+stepState)
					}
				}
				if len(summaries) > 0 {
					detail.Sections = append(detail.Sections, Section{Title: "Jobs", Markdown: strings.Join(summaries, "\n")})
				}
			}
		}
		if raw.Status != "completed" {
			return detail, nil
		}
		logs, err := g.api(ctx, "GET", "repos/"+g.repo+"/actions/runs/"+item.ID+"/logs")
		if err != nil {
			detail.Sections = append(detail.Sections, Section{Title: "Logs", Markdown: "_Logs unavailable._"})
			return detail, nil
		}
		logSections, err := githubLogSections(logs)
		if err != nil {
			detail.Sections = append(detail.Sections, Section{Title: "Logs", Markdown: "_Logs unavailable._"})
			return detail, nil
		}
		detail.Sections = append(detail.Sections, logSections...)
		return detail, nil
	default:
		return Detail{}, fmt.Errorf("unsupported GitHub detail kind: %s", kind)
	}
}

func githubLogSections(data []byte) ([]Section, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("decode GitHub Actions logs: %w", err)
	}
	sections := make([]Section, 0, len(archive.File))
	for _, file := range archive.File {
		if file.FileInfo().IsDir() {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open GitHub Actions log %q: %w", file.Name, err)
		}
		contents, readErr := io.ReadAll(io.LimitReader(reader, maxCILogBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read GitHub Actions log %q: %w", file.Name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close GitHub Actions log %q: %w", file.Name, closeErr)
		}
		sections = append(sections, Section{Title: "Logs · " + file.Name, Markdown: ciLogMarkdown(contents)})
	}
	return sections, nil
}

func (g *GitHub) githubCommitComments(ctx context.Context, sha string) ([]githubCommitComment, error) {
	data, err := g.api(ctx, "GET", "repos/"+g.repo+"/commits/"+sha+"/comments", "per_page=100")
	if err != nil {
		return nil, err
	}
	var comments []githubCommitComment
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, fmt.Errorf("decode GitHub commit comments: %w", err)
	}
	return comments, nil
}

func githubCommitPositionedComments(diffs []DiffFile, comments []githubCommitComment) []positionedDiffReview {
	positioned := make([]positionedDiffReview, 0, len(comments))
	for _, comment := range comments {
		review := DiffReview{Author: comment.User.Login, Body: comment.Body, CreatedAt: parseTime(comment.CreatedAt)}
		if line, ok := githubCommitCommentLine(diffs, comment); ok {
			if line.NewLine > 0 {
				review.NewLine = line.NewLine
				review.Side = ReviewSideNew
			} else {
				review.OldLine = line.OldLine
				review.Side = ReviewSideOld
			}
		}
		location := "commit"
		if comment.Path != "" {
			location = "`" + comment.Path + "`"
			if comment.Line != nil {
				location += fmt.Sprintf(":%d", *comment.Line)
			}
		}
		positioned = append(positioned, positionedDiffReview{
			OldPath: comment.Path,
			NewPath: comment.Path,
			Review:  review,
			Fallback: Section{
				Title:    "Commit comment · @" + comment.User.Login + " · " + location + " · " + displayTime(parseTime(comment.CreatedAt)),
				Markdown: comment.Body,
			},
		})
	}
	return positioned
}

func githubCommitCommentLine(diffs []DiffFile, comment githubCommitComment) (DiffLine, bool) {
	for _, file := range diffs {
		if comment.Path != file.NewPath && comment.Path != file.OldPath {
			continue
		}
		for _, line := range file.Lines {
			matchesPosition := comment.Position != nil && line.Position == *comment.Position
			matchesLine := comment.Position == nil && comment.Line != nil && (line.NewLine == *comment.Line || line.OldLine == *comment.Line)
			if matchesPosition || matchesLine {
				return line, true
			}
		}
	}
	return DiffLine{}, false
}

func (g *GitHub) githubPullRequestDiffs(ctx context.Context, id, baseSHA, headSHA string) ([]DiffFile, error) {
	host := g.host
	if host == "" {
		host = "github.com"
	}
	endpoint := "repos/" + g.repo + "/pulls/" + id + "/files?per_page=100"
	data, err := g.runner.Run(ctx, "gh", "api", "--hostname", host, "--method", "GET", endpoint, "--paginate", "--slurp")
	if err != nil {
		return nil, err
	}
	type githubDiffFile struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Patch            string `json:"patch"`
	}
	var pages [][]githubDiffFile
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("decode GitHub pull request files: %w", err)
	}
	var diffs []DiffFile
	for _, page := range pages {
		for _, file := range page {
			oldPath := file.PreviousFilename
			if oldPath == "" {
				oldPath = file.Filename
			}
			diffs = append(diffs, DiffFile{
				OldPath:  oldPath,
				NewPath:  file.Filename,
				Lines:    ParseUnifiedDiffLines(file.Patch),
				TooLarge: file.Patch == "",
				BaseSHA:  baseSHA,
				HeadSHA:  headSHA,
			})
		}
	}
	return diffs, nil
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

func (g *GitHub) githubReviewComments(ctx context.Context, id string) ([]positionedDiffReview, error) {
	host := g.host
	if host == "" {
		host = "github.com"
	}
	endpoint := "repos/" + g.repo + "/pulls/" + id + "/comments?per_page=100"
	data, err := g.runner.Run(ctx, "gh", "api", "--hostname", host, "--method", "GET", endpoint, "--paginate", "--slurp")
	if err != nil {
		return nil, err
	}
	var comments []githubReviewComment
	var pages [][]githubReviewComment
	if err := json.Unmarshal(data, &pages); err == nil {
		for _, page := range pages {
			comments = append(comments, page...)
		}
	} else if err := json.Unmarshal(data, &comments); err != nil {
		return nil, fmt.Errorf("decode GitHub review comments: %w", err)
	}
	if len(comments) == 0 {
		return nil, nil
	}
	threadInfo, err := g.githubReviewThreads(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load GitHub review thread capabilities: %w", err)
	}
	return githubPositionedReviews(comments, threadInfo), nil
}

func githubPositionedReviews(comments []githubReviewComment, threadInfo map[int64]githubReviewThreadInfo) []positionedDiffReview {
	reviews := make([]positionedDiffReview, 0, len(comments))
	for _, comment := range comments {
		location := "`" + comment.Path + "`"
		line := comment.Line
		fileLevel := strings.EqualFold(comment.SubjectType, "file")
		outdated := line == nil && !fileLevel
		if line == nil {
			line = comment.OriginalLine
		}
		startLine := comment.StartLine
		if startLine == nil {
			startLine = comment.OriginalStart
		}
		side := ReviewSideNew
		if strings.EqualFold(comment.Side, "LEFT") {
			side = ReviewSideOld
		}
		replyToID := comment.ID
		if comment.InReplyToID != nil {
			replyToID = *comment.InReplyToID
		}
		commentID := ""
		if comment.ID > 0 {
			commentID = strconv.FormatInt(comment.ID, 10)
		}
		replyID := ""
		if replyToID > 0 {
			replyID = strconv.FormatInt(replyToID, 10)
		}
		thread := threadInfo[comment.ID]
		if thread.ID == "" && comment.InReplyToID != nil {
			thread = threadInfo[*comment.InReplyToID]
		}
		review := DiffReview{
			ID:         commentID,
			ThreadID:   thread.ID,
			ReplyToID:  replyID,
			Author:     comment.User.Login,
			Body:       comment.Body,
			CreatedAt:  parseTime(comment.CreatedAt),
			Side:       side,
			Outdated:   outdated,
			FileLevel:  fileLevel,
			Resolved:   thread.Resolved,
			Resolvable: thread.Resolvable,
			Replyable:  thread.CanReply,
		}
		if line != nil {
			location += fmt.Sprintf(":%d", *line)
			if side == ReviewSideOld {
				review.OldLine = *line
			} else {
				review.NewLine = *line
			}
		}
		if startLine != nil {
			startSide := side
			if strings.EqualFold(comment.StartSide, "LEFT") {
				startSide = ReviewSideOld
			}
			if startSide == ReviewSideOld {
				review.StartOldLine = *startLine
			} else {
				review.StartNewLine = *startLine
			}
		}
		reviews = append(reviews, positionedDiffReview{
			OldPath: comment.Path,
			NewPath: comment.Path,
			Review:  review,
			Fallback: Section{
				Title:    "Inline comment · @" + comment.User.Login + " · " + location + " · " + displayTime(parseTime(comment.CreatedAt)),
				Markdown: comment.Body,
			},
		})
	}
	return reviews
}

func (g *GitHub) githubReviewThreads(ctx context.Context, id string) (map[int64]githubReviewThreadInfo, error) {
	owner, name, ok := strings.Cut(g.repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid GitHub repository %q", g.repo)
	}
	host := g.host
	if host == "" {
		host = "github.com"
	}
	query := `query($owner:String!,$name:String!,$number:Int!,$endCursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100,after:$endCursor){nodes{id isResolved viewerCanResolve viewerCanReply comments(first:100){nodes{databaseId}}}pageInfo{hasNextPage endCursor}}}}}`
	data, err := g.runner.Run(ctx, "gh", "api", "graphql", "--hostname", host, "--method", "POST", "--paginate", "--slurp", "-f", "query="+query, "-f", "owner="+owner, "-f", "name="+name, "-F", "number="+id)
	if err != nil {
		return nil, err
	}
	type page struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID               string `json:"id"`
							IsResolved       bool   `json:"isResolved"`
							ViewerCanResolve bool   `json:"viewerCanResolve"`
							ViewerCanReply   bool   `json:"viewerCanReply"`
							Comments         struct {
								Nodes []struct {
									DatabaseID int64 `json:"databaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	var pages []page
	if err := json.Unmarshal(data, &pages); err != nil {
		var single page
		if singleErr := json.Unmarshal(data, &single); singleErr != nil {
			return nil, fmt.Errorf("decode GitHub review threads: %w", err)
		}
		pages = []page{single}
	}
	info := make(map[int64]githubReviewThreadInfo)
	for _, current := range pages {
		for _, thread := range current.Data.Repository.PullRequest.ReviewThreads.Nodes {
			for _, comment := range thread.Comments.Nodes {
				info[comment.DatabaseID] = githubReviewThreadInfo{ID: thread.ID, Resolved: thread.IsResolved, Resolvable: thread.ViewerCanResolve, CanReply: thread.ViewerCanReply}
			}
		}
	}
	return info, nil
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

func (g *GitHub) CancelRun(ctx context.Context, item Item) error {
	if err := requireItem(CIRuns, item); err != nil {
		return err
	}
	_, err := g.api(ctx, "POST", "repos/"+g.repo+"/actions/runs/"+item.ID+"/cancel")
	return err
}

func (g *GitHub) Rerun(ctx context.Context, item Item) error {
	if err := requireItem(CIRuns, item); err != nil {
		return err
	}
	_, err := g.api(ctx, "POST", "repos/"+g.repo+"/actions/runs/"+item.ID+"/rerun")
	return err
}

func (g *GitHub) AddComment(ctx context.Context, kind Kind, item Item, body string) error {
	if err := requireCommentable(kind, item, body); err != nil {
		return err
	}
	if kind == Commits {
		_, err := g.api(ctx, "POST", "repos/"+g.repo+"/commits/"+item.ID+"/comments", "body="+body)
		return err
	}
	_, err := g.api(ctx, "POST", "repos/"+g.repo+"/issues/"+item.ID+"/comments", "body="+body)
	return err
}

func (g *GitHub) AddReview(ctx context.Context, item Item, body string) error {
	if err := requireReview(item, body); err != nil {
		return err
	}
	_, err := g.api(ctx, "POST", "repos/"+g.repo+"/pulls/"+item.ID+"/reviews", "body="+body, "event=COMMENT")
	return err
}

func (g *GitHub) AddReviewComment(ctx context.Context, item Item, target ReviewTarget, body string) error {
	if err := requireReviewComment(item, target, body); err != nil {
		return err
	}
	path := target.NewPath
	line, side := target.NewLine, "RIGHT"
	if target.reviewSide() == ReviewSideOld {
		line, side = target.OldLine, "LEFT"
	}
	if path == "" {
		path = target.OldPath
	}
	if path == "" {
		return fmt.Errorf("review target has no %s path", strings.ToLower(side))
	}
	host := g.host
	if host == "" {
		host = "github.com"
	}
	args := []string{
		"api", "--hostname", host, "--method", "POST", "repos/" + g.repo + "/pulls/" + item.ID + "/comments",
		"-f", "body=" + body,
		"-f", "commit_id=" + target.HeadSHA,
		"-f", "path=" + path,
	}
	if target.IsRange() {
		startLine := target.StartNewLine
		if target.reviewSide() == ReviewSideOld {
			startLine = target.StartOldLine
		}
		args = append(args, "-F", "start_line="+strconv.Itoa(startLine), "-f", "start_side="+side)
	}
	args = append(args, "-F", "line="+strconv.Itoa(line), "-f", "side="+side)
	_, err := g.runner.Run(ctx, "gh", args...)
	return err
}

func (g *GitHub) AddCommitComment(ctx context.Context, item Item, target ReviewTarget, body string) error {
	if err := requireCommitComment(item, target, body); err != nil {
		return err
	}
	if target.Position <= 0 {
		return fmt.Errorf("GitHub commit comment target has no diff position")
	}
	path := target.NewPath
	if path == "" {
		path = target.OldPath
	}
	host := g.host
	if host == "" {
		host = "github.com"
	}
	_, err := g.runner.Run(ctx, "gh",
		"api", "--hostname", host, "--method", "POST", "repos/"+g.repo+"/commits/"+item.ID+"/comments",
		"-f", "body="+body,
		"-f", "path="+path,
		"-F", "position="+strconv.Itoa(target.Position),
	)
	return err
}

func (g *GitHub) AddReviewReply(ctx context.Context, item Item, target ReviewThreadTarget, body string) error {
	if err := requireReviewReply(item, target, body); err != nil {
		return err
	}
	if target.ReplyToID == "" {
		return fmt.Errorf("GitHub review reply has no root comment ID")
	}
	endpoint := "repos/" + g.repo + "/pulls/" + item.ID + "/comments/" + url.PathEscape(target.ReplyToID) + "/replies"
	_, err := g.api(ctx, "POST", endpoint, "body="+body)
	return err
}

func (g *GitHub) ResolveReview(ctx context.Context, item Item, target ReviewThreadTarget) error {
	if err := requireResolvableReview(item, target); err != nil {
		return err
	}
	host := g.host
	if host == "" {
		host = "github.com"
	}
	query := `mutation($threadId:ID!){resolveReviewThread(input:{threadId:$threadId}){thread{id isResolved}}}`
	_, err := g.runner.Run(ctx, "gh", "api", "graphql", "--hostname", host, "--method", "POST", "-f", "query="+query, "-f", "threadId="+target.ThreadID)
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

func (g *GitHub) SetAssigned(ctx context.Context, kind Kind, item Item, assigned bool) error {
	if err := requireAssignable(kind, item); err != nil {
		return err
	}
	current, err := g.currentUser(ctx)
	if err != nil {
		return err
	}
	method := "POST"
	if !assigned {
		method = "DELETE"
	}
	_, err = g.api(ctx, method, "repos/"+g.repo+"/issues/"+item.ID+"/assignees", "assignees[]="+current.Login)
	return err
}

func (g *GitHub) currentUser(ctx context.Context) (Assignee, error) {
	g.userMu.Lock()
	defer g.userMu.Unlock()
	if g.user != nil {
		return *g.user, nil
	}
	data, err := g.api(ctx, "GET", "user")
	if err != nil {
		return Assignee{}, fmt.Errorf("resolve GitHub user: %w", err)
	}
	var raw githubUser
	if err := json.Unmarshal(data, &raw); err != nil {
		return Assignee{}, fmt.Errorf("decode GitHub user: %w", err)
	}
	if raw.Login == "" {
		return Assignee{}, fmt.Errorf("resolve GitHub user: response has no login")
	}
	id := ""
	if raw.ID > 0 {
		id = strconv.Itoa(raw.ID)
	}
	current := Assignee{ID: id, Login: raw.Login}
	g.user = &current
	return current, nil
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
