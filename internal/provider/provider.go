package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

type Kind int

const (
	PullRequests Kind = iota
	Issues
	Milestones
	Branches
	Commits
	CIRuns
)

func (k Kind) String() string {
	switch k {
	case PullRequests:
		return "Pull Requests"
	case Issues:
		return "Issues"
	case Milestones:
		return "Milestones"
	case Branches:
		return "Branches"
	case Commits:
		return "Commits"
	case CIRuns:
		return "CI Runs"
	default:
		return "Unknown"
	}
}

type Filter struct {
	Label string
	Value string
}

type Assignee struct {
	ID    string
	Login string
}

// CommitRef identifies a branch or tag pointing at a commit.
type CommitRef struct {
	Name   string
	Remote bool
	Head   bool
	Tag    bool
}

type Item struct {
	ID           string
	Title        string
	State        string
	Author       string
	Assignees    []Assignee
	AssignedToMe bool
	HeadSHA      string
	UpdatedAt    time.Time
	Meta         string
	URL          string
	Parents      []string
	Refs         []CommitRef
}

type Section struct {
	Title    string
	Markdown string
}

const maxCILogBytes = 2 << 20

func ciLogMarkdown(data []byte) string {
	truncated := len(data) > maxCILogBytes
	if truncated {
		data = data[:maxCILogBytes]
	}
	text := sanitizeCILog(string(data))
	markdown := "    " + strings.ReplaceAll(text, "\n", "\n    ")
	if truncated {
		markdown += "\n\n_Log truncated after 2 MiB._"
	}
	return markdown
}

func sanitizeCILog(text string) string {
	text = ansi.Strip(strings.ReplaceAll(text, "\r\n", "\n"))
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, text)
}

type Detail struct {
	Item     Item
	Body     string
	Sections []Section
	Labels   []string
	Diffs    []DiffFile
}

type DiffLine struct {
	OldLine     int
	NewLine     int
	OldPosition int
	NewPosition int
	Position    int
	Text        string
}

type DiffFile struct {
	OldPath  string
	NewPath  string
	Lines    []DiffLine
	Reviews  []DiffReview
	TooLarge bool
	BaseSHA  string
	StartSHA string
	HeadSHA  string
}

type DiffReview struct {
	ID           string
	ThreadID     string
	ReplyToID    string
	Author       string
	Body         string
	CreatedAt    time.Time
	StartOldLine int
	StartNewLine int
	OldLine      int
	NewLine      int
	Side         ReviewSide
	Outdated     bool
	FileLevel    bool
	Resolved     bool
	Resolvable   bool
	Replyable    bool
}

type ReviewThreadTarget struct {
	ThreadID  string
	ReplyToID string
}

type positionedDiffReview struct {
	OldPath  string
	NewPath  string
	Review   DiffReview
	Fallback Section
}

func attachDiffReviews(diffs []DiffFile, reviews []positionedDiffReview) []Section {
	var unpositioned []Section
	for _, review := range reviews {
		attached := false
		if review.Review.FileLevel || review.Review.OldLine > 0 || review.Review.NewLine > 0 {
			for index := range diffs {
				if review.NewPath != "" && review.NewPath == diffs[index].NewPath || review.OldPath != "" && review.OldPath == diffs[index].OldPath {
					diffs[index].Reviews = append(diffs[index].Reviews, review.Review)
					attached = true
					break
				}
			}
		}
		if !attached {
			unpositioned = append(unpositioned, review.Fallback)
		}
	}
	return unpositioned
}

type ReviewSide string

const (
	ReviewSideOld ReviewSide = "old"
	ReviewSideNew ReviewSide = "new"
)

type ReviewTarget struct {
	OldPath          string
	NewPath          string
	StartOldLine     int
	StartNewLine     int
	OldLine          int
	NewLine          int
	StartOldPosition int
	StartNewPosition int
	OldPosition      int
	NewPosition      int
	Position         int
	Side             ReviewSide
	BaseSHA          string
	StartSHA         string
	HeadSHA          string
}

func (t ReviewTarget) IsRange() bool {
	switch t.reviewSide() {
	case ReviewSideOld:
		return t.StartOldLine > 0 && t.OldLine > 0 && t.StartOldLine != t.OldLine
	case ReviewSideNew:
		return t.StartNewLine > 0 && t.NewLine > 0 && t.StartNewLine != t.NewLine
	default:
		return false
	}
}

func (t ReviewTarget) reviewSide() ReviewSide {
	if t.Side != "" {
		return t.Side
	}
	if t.NewLine > 0 {
		return ReviewSideNew
	}
	return ReviewSideOld
}

type Provider interface {
	Name() string
	Repository() string
	TabName(Kind) string
	Filters(Kind) []Filter
	List(context.Context, Kind, Filter) ([]Item, error)
	Detail(context.Context, Kind, Item) (Detail, error)
	AddComment(context.Context, Kind, Item, string) error
	AddReview(context.Context, Item, string) error
	AddReviewComment(context.Context, Item, ReviewTarget, string) error
	AddCommitComment(context.Context, Item, ReviewTarget, string) error
	AddReviewReply(context.Context, Item, ReviewThreadTarget, string) error
	ResolveReview(context.Context, Item, ReviewThreadTarget) error
	Merge(context.Context, Item) error
	SetIssueState(context.Context, Item, bool) error
	SetAssigned(context.Context, Kind, Item, bool) error
	SetIssueLabels(context.Context, Item, []string) error
	CancelRun(context.Context, Item) error
	Rerun(context.Context, Item) error
}

// ParseUnifiedDiffLines converts selectable unified-diff content into lines with
// their corresponding old and new file positions. Hunk headers and metadata are
// intentionally omitted.
func ParseUnifiedDiffLines(patch string) []DiffLine {
	var lines []DiffLine
	oldLine, newLine := 0, 0
	inHunk := false
	position := 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if inHunk {
				position++
			}
			oldLine, newLine, inHunk = parseHunkStarts(line)
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		position++
		switch line[0] {
		case ' ':
			lines = append(lines, DiffLine{OldLine: oldLine, NewLine: newLine, OldPosition: oldLine, NewPosition: newLine, Position: position, Text: line})
			oldLine++
			newLine++
		case '-':
			lines = append(lines, DiffLine{OldLine: oldLine, OldPosition: oldLine, NewPosition: newLine, Position: position, Text: line})
			oldLine++
		case '+':
			lines = append(lines, DiffLine{NewLine: newLine, OldPosition: oldLine, NewPosition: newLine, Position: position, Text: line})
			newLine++
		case '\\':
			// "No newline at end of file" is metadata, not a selectable line.
		}
	}
	return lines
}

func parseHunkStarts(header string) (oldLine, newLine int, ok bool) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[0], "@@") {
		return 0, 0, false
	}
	oldLine, oldOK := parseDiffRangeStart(fields[1], '-')
	newLine, newOK := parseDiffRangeStart(fields[2], '+')
	return oldLine, newLine, oldOK && newOK
}

func parseDiffRangeStart(value string, prefix byte) (int, bool) {
	if len(value) < 2 || value[0] != prefix {
		return 0, false
	}
	start := strings.SplitN(value[1:], ",", 2)[0]
	line, err := strconv.Atoi(start)
	return line, err == nil
}

type AuthError struct {
	Provider string
	Command  string
	Cause    error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("%s authentication is not ready; run %q: %v", e.Provider, e.Command, e.Cause)
}

func (e *AuthError) Unwrap() error { return e.Cause }

func ParseRepositoryURL(raw string) (host, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(raw, "git@"), ":", 2)
		if len(parts) == 2 {
			return parts[0], strings.Trim(parts[1], "/"), true
		}
	}
	for _, prefix := range []string{"https://", "http://", "ssh://"} {
		if strings.HasPrefix(raw, prefix) {
			withoutScheme := strings.TrimPrefix(raw, prefix)
			if at := strings.LastIndex(withoutScheme, "@"); at >= 0 {
				withoutScheme = withoutScheme[at+1:]
			}
			parts := strings.SplitN(withoutScheme, "/", 2)
			if len(parts) == 2 {
				host = strings.Split(parts[0], ":")[0]
				return host, strings.Trim(parts[1], "/"), true
			}
		}
	}
	return "", "", false
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

func requireItem(kind Kind, item Item) error {
	if item.ID == "" {
		return fmt.Errorf("%s item has no identifier", kind)
	}
	return nil
}

func requireCommentable(kind Kind, item Item, body string) error {
	if kind != PullRequests && kind != Issues && kind != Commits {
		return fmt.Errorf("%s items do not support comments", kind)
	}
	if err := requireItem(kind, item); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("comment body cannot be empty")
	}
	return nil
}

func requireCommitComment(item Item, target ReviewTarget, body string) error {
	if err := requireCommentable(Commits, item, body); err != nil {
		return err
	}
	if target.IsRange() {
		return fmt.Errorf("commit comments support one diff line")
	}
	if target.OldLine <= 0 && target.NewLine <= 0 {
		return fmt.Errorf("commit comment target has no valid line")
	}
	if strings.TrimSpace(target.OldPath) == "" && strings.TrimSpace(target.NewPath) == "" {
		return fmt.Errorf("commit comment target has no path")
	}
	return nil
}

func requireReviewComment(item Item, target ReviewTarget, body string) error {
	if err := requireReview(item, body); err != nil {
		return err
	}
	if target.OldLine < 0 || target.NewLine < 0 || target.OldLine == 0 && target.NewLine == 0 {
		return fmt.Errorf("review target has no valid line")
	}
	switch target.reviewSide() {
	case ReviewSideOld:
		if target.OldLine == 0 {
			return fmt.Errorf("review target has no old line")
		}
	case ReviewSideNew:
		if target.NewLine == 0 {
			return fmt.Errorf("review target has no new line")
		}
	default:
		return fmt.Errorf("review target has invalid side %q", target.Side)
	}
	if target.HeadSHA == "" {
		return fmt.Errorf("review target has no head SHA")
	}
	return nil
}

func requireReview(item Item, body string) error {
	if err := requireItem(PullRequests, item); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("review body cannot be empty")
	}
	return nil
}

func requireReviewReply(item Item, target ReviewThreadTarget, body string) error {
	if err := requireReview(item, body); err != nil {
		return err
	}
	if target.ThreadID == "" && target.ReplyToID == "" {
		return fmt.Errorf("review reply has no thread target")
	}
	return nil
}

func requireResolvableReview(item Item, target ReviewThreadTarget) error {
	if err := requireItem(PullRequests, item); err != nil {
		return err
	}
	if target.ThreadID == "" {
		return fmt.Errorf("review has no resolvable thread")
	}
	return nil
}

func requireAssignable(kind Kind, item Item) error {
	if kind != PullRequests && kind != Issues {
		return fmt.Errorf("%s items cannot be assigned", kind)
	}
	return requireItem(kind, item)
}

func hasAssignee(assignees []Assignee, target Assignee) bool {
	for _, assignee := range assignees {
		if sameAssignee(assignee, target) {
			return true
		}
	}
	return false
}

func sameAssignee(left, right Assignee) bool {
	if left.ID != "" && right.ID != "" {
		return left.ID == right.ID
	}
	return strings.EqualFold(left.Login, right.Login)
}
