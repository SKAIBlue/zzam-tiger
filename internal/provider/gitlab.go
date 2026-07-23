package provider

import (
	"context"
	"crypto/sha1"
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
	if kind == CIRuns {
		return "Pipelines"
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
	case CIRuns:
		return []Filter{{"All", "all"}, {"Running", "running"}, {"Pending", "pending"}, {"Success", "success"}, {"Failure", "failed"}}
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

type gitlabPipeline struct {
	ID        int64      `json:"id"`
	IID       int64      `json:"iid"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	Source    string     `json:"source"`
	Ref       string     `json:"ref"`
	SHA       string     `json:"sha"`
	WebURL    string     `json:"web_url"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	User      gitlabUser `json:"user"`
}

type gitlabJob struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

func (p gitlabPipeline) item() Item {
	title := p.Name
	if title == "" {
		title = p.Ref
	}
	author := p.User.Username
	if author == "" {
		author = p.User.Name
	}
	meta := p.Ref
	if p.Source != "" {
		meta += " · " + p.Source
	}
	updated := p.UpdatedAt
	if updated == "" {
		updated = p.CreatedAt
	}
	return Item{ID: strconv.FormatInt(p.ID, 10), Title: title, State: p.Status, Author: author, HeadSHA: p.SHA, UpdatedAt: parseTime(updated), Meta: meta, URL: p.WebURL}
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
	case CIRuns:
		endpoint := base + "/pipelines?per_page=100&order_by=updated_at&sort=desc"
		if filter.Value != "" && filter.Value != "all" {
			endpoint += "&status=" + url.QueryEscape(filter.Value)
		}
		data, err := g.api(ctx, "GET", endpoint)
		if err != nil {
			return nil, err
		}
		var raw []gitlabPipeline
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode GitLab pipelines: %w", err)
		}
		items := make([]Item, 0, len(raw))
		for _, pipeline := range raw {
			items = append(items, pipeline.item())
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
	ID               int64                 `json:"id"`
	Body             string                `json:"body"`
	System           bool                  `json:"system"`
	Resolvable       bool                  `json:"resolvable"`
	Resolved         bool                  `json:"resolved"`
	CreatedAt        string                `json:"created_at"`
	Author           gitlabUser            `json:"author"`
	Position         *gitlabReviewPosition `json:"position"`
	OriginalPosition *gitlabReviewPosition `json:"original_position"`
}

type gitlabDiscussion struct {
	ID    string       `json:"id"`
	Notes []gitlabNote `json:"notes"`
}

type gitlabReviewLine struct {
	LineCode string `json:"line_code"`
	Type     string `json:"type"`
	OldLine  int    `json:"old_line,omitempty"`
	NewLine  int    `json:"new_line,omitempty"`
}

type gitlabReviewLineRange struct {
	Start gitlabReviewLine `json:"start"`
	End   gitlabReviewLine `json:"end"`
}

type gitlabReviewPosition struct {
	PositionType string                 `json:"position_type"`
	OldPath      string                 `json:"old_path"`
	NewPath      string                 `json:"new_path"`
	OldLine      int                    `json:"old_line,omitempty"`
	NewLine      int                    `json:"new_line,omitempty"`
	BaseSHA      string                 `json:"base_sha"`
	StartSHA     string                 `json:"start_sha"`
	HeadSHA      string                 `json:"head_sha"`
	LineRange    *gitlabReviewLineRange `json:"line_range,omitempty"`
}

type gitlabCommitComment struct {
	Note      string     `json:"note"`
	Path      string     `json:"path"`
	Line      *int       `json:"line"`
	LineType  string     `json:"line_type"`
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
			diffs, err := g.gitlabMergeRequestDiffs(ctx, item.ID)
			if err != nil {
				return Detail{}, err
			}
			detail.Diffs = diffs
		}
		notes, reviews, err := g.discussions(ctx, resource, item.ID)
		if err != nil {
			return Detail{}, err
		}
		detail.Sections = append(detail.Sections, notes...)
		if kind == PullRequests {
			detail.Sections = append(detail.Sections, attachDiffReviews(detail.Diffs, reviews)...)
		}
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
		diffData, err := g.api(ctx, "GET", base+"/repository/commits/"+item.ID+"/diff?per_page=100&unidiff=true")
		if err != nil {
			return Detail{}, err
		}
		var diffs []struct {
			NewPath     string `json:"new_path"`
			OldPath     string `json:"old_path"`
			NewFile     bool   `json:"new_file"`
			RenamedFile bool   `json:"renamed_file"`
			DeletedFile bool   `json:"deleted_file"`
			Diff        string `json:"diff"`
			Collapsed   bool   `json:"collapsed"`
			TooLarge    bool   `json:"too_large"`
		}
		if err := json.Unmarshal(diffData, &diffs); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab commit diff: %w", err)
		}
		files := make([]string, 0, len(diffs))
		diffFiles := make([]DiffFile, 0, len(diffs))
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
			diffFiles = append(diffFiles, DiffFile{
				OldPath:  diff.OldPath,
				NewPath:  diff.NewPath,
				Lines:    ParseUnifiedDiffLines(diff.Diff),
				TooLarge: diff.TooLarge || diff.Collapsed || diff.Diff == "",
				HeadSHA:  raw.ID,
			})
		}
		detail := Detail{Item: raw.item(), Body: raw.Message, Sections: []Section{{Title: "Stats", Markdown: fmt.Sprintf("**%d** files changed · +%d / -%d", raw.Stats.Total, raw.Stats.Additions, raw.Stats.Deletions)}, {Title: "Files", Markdown: strings.Join(files, "\n")}}, Diffs: diffFiles}
		comments, err := g.gitlabCommitComments(ctx, item.ID)
		if err != nil {
			return Detail{}, err
		}
		detail.Sections = append(detail.Sections, attachDiffReviews(detail.Diffs, gitlabCommitPositionedComments(comments))...)
		return detail, nil
	case CIRuns:
		data, err := g.api(ctx, "GET", base+"/pipelines/"+item.ID)
		if err != nil {
			return Detail{}, err
		}
		var raw gitlabPipeline
		if err := json.Unmarshal(data, &raw); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab pipeline: %w", err)
		}
		detail := Detail{
			Item: raw.item(),
			Body: fmt.Sprintf("- Source: `%s`\n- Ref: `%s`\n- Commit: `%s`", raw.Source, raw.Ref, raw.SHA),
		}
		jobData, err := g.api(ctx, "GET", base+"/pipelines/"+item.ID+"/jobs?per_page=100")
		if err != nil {
			return Detail{}, err
		}
		var jobs []gitlabJob
		if err := json.Unmarshal(jobData, &jobs); err != nil {
			return Detail{}, fmt.Errorf("decode GitLab pipeline jobs: %w", err)
		}
		for _, job := range jobs {
			log := "_No log output yet._"
			if job.StartedAt != "" {
				trace, err := g.api(ctx, "GET", base+"/jobs/"+strconv.FormatInt(job.ID, 10)+"/trace")
				if err != nil {
					log = "_Logs unavailable._"
				} else if len(trace) > 0 {
					log = ciLogMarkdown(trace)
				}
			}
			title := "Logs · " + job.Name + " · " + job.Status
			if job.Stage != "" {
				title = "Logs · " + job.Stage + " / " + job.Name + " · " + job.Status
			}
			detail.Sections = append(detail.Sections, Section{Title: title, Markdown: log})
		}
		return detail, nil
	default:
		return Detail{}, fmt.Errorf("unsupported GitLab detail kind: %s", kind)
	}
}

func (g *GitLab) gitlabCommitComments(ctx context.Context, sha string) ([]gitlabCommitComment, error) {
	data, err := g.api(ctx, "GET", "projects/"+g.project+"/repository/commits/"+sha+"/comments?per_page=100")
	if err != nil {
		return nil, err
	}
	var comments []gitlabCommitComment
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, fmt.Errorf("decode GitLab commit comments: %w", err)
	}
	return comments, nil
}

func gitlabCommitPositionedComments(comments []gitlabCommitComment) []positionedDiffReview {
	positioned := make([]positionedDiffReview, 0, len(comments))
	for _, comment := range comments {
		review := DiffReview{Author: comment.Author.Username, Body: comment.Note, CreatedAt: parseTime(comment.CreatedAt)}
		if comment.Line != nil {
			if comment.LineType == "old" {
				review.OldLine = *comment.Line
				review.Side = ReviewSideOld
			} else {
				review.NewLine = *comment.Line
				review.Side = ReviewSideNew
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
				Title:    "Commit comment · @" + comment.Author.Username + " · " + location + " · " + displayTime(parseTime(comment.CreatedAt)),
				Markdown: comment.Note,
			},
		})
	}
	return positioned
}

func (g *GitLab) gitlabMergeRequestDiffs(ctx context.Context, id string) ([]DiffFile, error) {
	base := "projects/" + g.project + "/merge_requests/" + id
	host := g.host
	if host == "" {
		host = "gitlab.com"
	}
	data, err := g.runner.Run(ctx, "glab", "api", base+"/diffs?per_page=100&unidiff=true", "--hostname", host, "--method", "GET", "--paginate", "--output", "json")
	if err != nil {
		return nil, err
	}
	var rawDiffs []struct {
		OldPath   string `json:"old_path"`
		NewPath   string `json:"new_path"`
		Diff      string `json:"diff"`
		TooLarge  bool   `json:"too_large"`
		Collapsed bool   `json:"collapsed"`
	}
	if err := json.Unmarshal(data, &rawDiffs); err != nil {
		return nil, fmt.Errorf("decode GitLab merge request diffs: %w", err)
	}
	versionData, err := g.api(ctx, "GET", base+"/versions?per_page=1")
	if err != nil {
		return nil, err
	}
	var versions []struct {
		BaseSHA  string `json:"base_commit_sha"`
		StartSHA string `json:"start_commit_sha"`
		HeadSHA  string `json:"head_commit_sha"`
	}
	if err := json.Unmarshal(versionData, &versions); err != nil {
		return nil, fmt.Errorf("decode GitLab merge request versions: %w", err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("GitLab merge request %s has no diff version", id)
	}
	version := versions[0]
	diffs := make([]DiffFile, 0, len(rawDiffs))
	for _, diff := range rawDiffs {
		diffs = append(diffs, DiffFile{
			OldPath:  diff.OldPath,
			NewPath:  diff.NewPath,
			Lines:    ParseUnifiedDiffLines(diff.Diff),
			TooLarge: diff.TooLarge || diff.Collapsed,
			BaseSHA:  version.BaseSHA,
			StartSHA: version.StartSHA,
			HeadSHA:  version.HeadSHA,
		})
	}
	return diffs, nil
}

func (g *GitLab) discussions(ctx context.Context, resource, id string) ([]Section, []positionedDiffReview, error) {
	var discussions []gitlabDiscussion
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("projects/%s/%s/%s/discussions?per_page=100&page=%d", g.project, resource, id, page)
		data, err := g.api(ctx, "GET", endpoint)
		if err != nil {
			return nil, nil, err
		}
		var current []gitlabDiscussion
		if err := json.Unmarshal(data, &current); err != nil {
			return nil, nil, fmt.Errorf("decode GitLab discussions page %d: %w", page, err)
		}
		discussions = append(discussions, current...)
		if len(current) < 100 {
			break
		}
	}
	sections := make([]Section, 0, len(discussions))
	var reviews []positionedDiffReview
	for _, discussion := range discussions {
		var threadPosition *gitlabReviewPosition
		threadOutdated := false
		threadResolvable := false
		threadResolved := false
		for _, note := range discussion.Notes {
			if note.Resolvable {
				threadResolvable = true
				threadResolved = note.Resolved
			}
			if note.Position != nil {
				threadPosition = note.Position
				break
			}
			if note.OriginalPosition != nil && threadPosition == nil {
				threadPosition = note.OriginalPosition
				threadOutdated = true
			}
		}
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
			fallback := Section{Title: kind + " · @" + author + " · " + displayTime(parseTime(note.CreatedAt)), Markdown: note.Body}
			if note.System || threadPosition == nil {
				sections = append(sections, fallback)
				continue
			}
			position := threadPosition
			outdated := threadOutdated
			if note.Position != nil {
				position = note.Position
				outdated = false
			} else if note.OriginalPosition != nil {
				position = note.OriginalPosition
				outdated = true
			}
			noteID := ""
			if note.ID > 0 {
				noteID = strconv.FormatInt(note.ID, 10)
			}
			review := DiffReview{
				ID:         noteID,
				ThreadID:   discussion.ID,
				Author:     author,
				Body:       note.Body,
				CreatedAt:  parseTime(note.CreatedAt),
				OldLine:    position.OldLine,
				NewLine:    position.NewLine,
				Outdated:   outdated,
				Resolved:   threadResolved,
				Resolvable: threadResolvable,
				Replyable:  true,
				Side:       ReviewSideOld,
			}
			if position.NewLine > 0 {
				review.Side = ReviewSideNew
			}
			if position.LineRange != nil {
				review.StartOldLine = position.LineRange.Start.OldLine
				review.StartNewLine = position.LineRange.Start.NewLine
				review.OldLine = position.LineRange.End.OldLine
				review.NewLine = position.LineRange.End.NewLine
			}
			reviews = append(reviews, positionedDiffReview{OldPath: position.OldPath, NewPath: position.NewPath, Review: review, Fallback: fallback})
		}
	}
	return sections, reviews, nil
}

func (g *GitLab) Merge(ctx context.Context, item Item) error {
	if item.HeadSHA == "" {
		return fmt.Errorf("refusing to merge merge request %s without a head SHA", item.ID)
	}
	fields := []string{"sha=" + item.HeadSHA}
	_, err := g.api(ctx, "PUT", "projects/"+g.project+"/merge_requests/"+item.ID+"/merge", fields...)
	return err
}

func (g *GitLab) CancelRun(ctx context.Context, item Item) error {
	if err := requireItem(CIRuns, item); err != nil {
		return err
	}
	_, err := g.api(ctx, "POST", "projects/"+g.project+"/pipelines/"+item.ID+"/cancel")
	return err
}

func (g *GitLab) Rerun(ctx context.Context, item Item) error {
	if err := requireItem(CIRuns, item); err != nil {
		return err
	}
	_, err := g.api(ctx, "POST", "projects/"+g.project+"/pipelines/"+item.ID+"/retry")
	return err
}

func (g *GitLab) AddComment(ctx context.Context, kind Kind, item Item, body string) error {
	if err := requireCommentable(kind, item, body); err != nil {
		return err
	}
	if kind == Commits {
		_, err := g.api(ctx, "POST", "projects/"+g.project+"/repository/commits/"+item.ID+"/comments", "note="+body)
		return err
	}
	resource := "issues"
	if kind == PullRequests {
		resource = "merge_requests"
	}
	_, err := g.api(ctx, "POST", "projects/"+g.project+"/"+resource+"/"+item.ID+"/notes", "body="+body)
	return err
}

func (g *GitLab) AddReview(ctx context.Context, item Item, body string) error {
	if err := requireReview(item, body); err != nil {
		return err
	}
	_, err := g.api(ctx, "POST", "projects/"+g.project+"/merge_requests/"+item.ID+"/notes", "body="+body)
	return err
}

func (g *GitLab) AddReviewComment(ctx context.Context, item Item, target ReviewTarget, body string) error {
	if err := requireReviewComment(item, target, body); err != nil {
		return err
	}
	if target.OldPath == "" || target.NewPath == "" {
		return fmt.Errorf("review target requires old and new paths")
	}
	if target.BaseSHA == "" || target.StartSHA == "" {
		return fmt.Errorf("review target has incomplete diff version")
	}
	payload := struct {
		Body     string               `json:"body"`
		Position gitlabReviewPosition `json:"position"`
	}{
		Body: body,
		Position: gitlabReviewPosition{
			PositionType: "text",
			OldPath:      target.OldPath,
			NewPath:      target.NewPath,
			OldLine:      target.OldLine,
			NewLine:      target.NewLine,
			BaseSHA:      target.BaseSHA,
			StartSHA:     target.StartSHA,
			HeadSHA:      target.HeadSHA,
		},
	}
	if target.IsRange() {
		path := target.NewPath
		if path == "" {
			path = target.OldPath
		}
		payload.Position.LineRange = &gitlabReviewLineRange{
			Start: gitlabRangeLine(path, target.StartOldLine, target.StartNewLine, target.StartOldPosition, target.StartNewPosition),
			End:   gitlabRangeLine(path, target.OldLine, target.NewLine, target.OldPosition, target.NewPosition),
		}
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode GitLab review comment: %w", err)
	}
	host := g.host
	if host == "" {
		host = "gitlab.com"
	}
	runner, ok := g.runner.(InputRunner)
	if !ok {
		return fmt.Errorf("GitLab review comments require a runner that supports standard input")
	}
	args := []string{"api", "projects/" + g.project + "/merge_requests/" + item.ID + "/discussions", "--hostname", host, "--method", "POST", "--input", "-", "--header", "Content-Type: application/json"}
	_, err = runner.RunInput(ctx, input, "glab", args...)
	return err
}

func (g *GitLab) AddCommitComment(ctx context.Context, item Item, target ReviewTarget, body string) error {
	if err := requireCommitComment(item, target, body); err != nil {
		return err
	}
	path := target.NewPath
	line := target.NewLine
	lineType := "new"
	if target.reviewSide() == ReviewSideOld {
		path = target.OldPath
		line = target.OldLine
		lineType = "old"
	}
	data, err := g.api(ctx, "POST", "projects/"+g.project+"/repository/commits/"+item.ID+"/comments",
		"note="+body,
		"path="+path,
		"line="+strconv.Itoa(line),
		"line_type="+lineType,
	)
	if err != nil {
		return err
	}
	var created gitlabCommitComment
	if err := json.Unmarshal(data, &created); err != nil {
		return fmt.Errorf("decode GitLab commit comment response: %w", err)
	}
	if created.Path != path || created.Line == nil || *created.Line != line || created.LineType != lineType {
		return fmt.Errorf("GitLab created the comment without the requested %s:%d anchor", path, line)
	}
	return nil
}

func (g *GitLab) AddReviewReply(ctx context.Context, item Item, target ReviewThreadTarget, body string) error {
	if err := requireReviewReply(item, target, body); err != nil {
		return err
	}
	if target.ThreadID == "" {
		return fmt.Errorf("GitLab review reply has no discussion ID")
	}
	endpoint := "projects/" + g.project + "/merge_requests/" + item.ID + "/discussions/" + url.PathEscape(target.ThreadID) + "/notes"
	_, err := g.api(ctx, "POST", endpoint, "body="+body)
	return err
}

func (g *GitLab) ResolveReview(ctx context.Context, item Item, target ReviewThreadTarget) error {
	if err := requireResolvableReview(item, target); err != nil {
		return err
	}
	endpoint := "projects/" + g.project + "/merge_requests/" + item.ID + "/discussions/" + url.PathEscape(target.ThreadID)
	_, err := g.api(ctx, "PUT", endpoint, "resolved=true")
	return err
}

func gitlabRangeLine(path string, oldLine, newLine, oldPosition, newPosition int) gitlabReviewLine {
	lineType := "old"
	if oldLine == 0 {
		lineType = "new"
	}
	return gitlabReviewLine{
		LineCode: fmt.Sprintf("%x_%d_%d", sha1.Sum([]byte(path)), oldPosition, newPosition),
		Type:     lineType,
		OldLine:  oldLine,
		NewLine:  newLine,
	}
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
