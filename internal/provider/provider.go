package provider

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Kind int

const (
	PullRequests Kind = iota
	Issues
	Milestones
	Branches
	Commits
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
	default:
		return "Unknown"
	}
}

type Filter struct {
	Label string
	Value string
}

type Item struct {
	ID        string
	Title     string
	State     string
	Author    string
	HeadSHA   string
	UpdatedAt time.Time
	Meta      string
	URL       string
}

type Section struct {
	Title    string
	Markdown string
}

type Detail struct {
	Item     Item
	Body     string
	Sections []Section
	Labels   []string
}

type Provider interface {
	Name() string
	Repository() string
	TabName(Kind) string
	Filters(Kind) []Filter
	List(context.Context, Kind, Filter) ([]Item, error)
	Detail(context.Context, Kind, Item) (Detail, error)
	Merge(context.Context, Item) error
	SetIssueState(context.Context, Item, bool) error
	SetIssueLabels(context.Context, Item, []string) error
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
