// Package worktree exposes local Git working-tree operations for the TUI.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
)

// Client reads and mutates one local Git working tree.
type Client struct {
	root       string
	runner     provider.Runner
	filesystem bool
}

// NewFilesystem creates a read-only filesystem browser rooted at root.
// Unlike New, it does not require Git and does not apply .gitignore rules.
func NewFilesystem(root string) *Client {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &Client{root: filepath.Clean(root), filesystem: true}
}

// Open creates a client only when root belongs to a readable Git working tree.
func Open(root string, runner provider.Runner) (*Client, error) {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	topLevel, err := runner.Run(ctx, "git", "-C", root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("Git repository required; run zt inside a Git working tree: %w", err)
	}
	resolvedRoot := strings.TrimSpace(string(topLevel))
	if resolvedRoot == "" {
		return nil, fmt.Errorf("Git repository required; git returned an empty working-tree root")
	}
	return New(resolvedRoot, runner), nil
}

// Root returns the normalized Git top-level managed by this client.
func (c *Client) Root() string { return c.root }

// Entry is an immediate file or directory in the working tree.
type Entry struct {
	Path  string
	Name  string
	IsDir bool
}

// File is the read-only representation of a working-tree file.
type File struct {
	Path      string
	Data      []byte
	Binary    bool
	Image     bool
	MIME      string
	Truncated bool
}

// Change describes one staged, unstaged, or untracked path.
type Change struct {
	Path     string
	OldPath  string
	Code     byte
	Unmerged bool
}

// Status separates changes by the action available to the commit UI.
type Status struct {
	Staged    []Change
	Unstaged  []Change
	Untracked []Change
}

// Diff contains both sides and Git's patch text for a selected path.
type Diff struct {
	Path   string
	Old    []byte
	New    []byte
	Binary bool
	Patch  string
}

// Ref identifies a branch or tag pointing at a commit.
type Ref struct {
	Name   string
	Remote bool
	Head   bool
	Tag    bool
}

// Commit is one entry in the repository's reachable history.
type Commit struct {
	SHA         string
	Parents     []string
	Subject     string
	Author      string
	AuthorEmail string
	Paths       []string
	AuthoredAt  time.Time
	Refs        []Ref
}

// Branch is a local or remote ref available from the Git repository.
type Branch struct {
	Name      string
	SHA       string
	Author    string
	UpdatedAt time.Time
	Head      bool
	Remote    bool
}

// New creates a client rooted at root. The root may be relative.
func New(root string, runner provider.Runner) *Client {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if topLevel, err := runner.Run(ctx, "git", "-C", root, "rev-parse", "--show-toplevel"); err == nil {
		if resolvedRoot := strings.TrimSpace(string(topLevel)); resolvedRoot != "" {
			root = resolvedRoot
		}
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &Client{root: filepath.Clean(root), runner: runner}
}

// Entries lists only the immediate children of a repository-relative directory.
// An empty path selects the working-tree root.
func (c *Client) Entries(ctx context.Context, path string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	abs := c.root
	clean := ""
	if path != "" {
		var err error
		abs, clean, err = c.resolve(path)
		if err != nil {
			return nil, err
		}
	}
	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(dirEntries))
	paths := make([]string, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		if dirEntry.Name() == ".git" {
			continue
		}
		entryPath := filepath.ToSlash(filepath.Join(clean, dirEntry.Name()))
		entries = append(entries, Entry{Path: entryPath, Name: dirEntry.Name(), IsDir: dirEntry.IsDir()})
		paths = append(paths, entryPath)
	}

	ignored := make(map[string]bool)
	if !c.filesystem {
		var err error
		ignored, err = c.ignoredEntries(ctx, paths)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	visible := entries[:0]
	for _, entry := range entries {
		if !ignored[entry.Path] {
			visible = append(visible, entry)
		}
	}
	return visible, nil
}

func (c *Client) ignoredEntries(ctx context.Context, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	if len(paths) == 0 {
		return ignored, nil
	}
	if runner, ok := c.runner.(provider.InputRunner); ok {
		input := []byte(strings.Join(paths, "\x00") + "\x00")
		out, err := runner.RunInput(ctx, input, "git", "-C", c.root, "check-ignore", "-z", "--stdin")
		if err != nil {
			if provider.IsExitCode(err, 1) {
				return ignored, nil
			}
			return nil, err
		}
		for _, path := range splitNUL(out) {
			ignored[filepath.ToSlash(path)] = true
		}
		return ignored, nil
	}
	for _, path := range paths {
		_, err := c.git(ctx, "check-ignore", "-q", "--", path)
		if err == nil {
			ignored[path] = true
		} else if !provider.IsExitCode(err, 1) {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return ignored, nil
}

// Read reads a working-tree file without interpreting its contents.
const maxPreviewBytes = 8 << 20

func (c *Client) Read(ctx context.Context, path string) (File, error) {
	abs, clean, err := c.resolve(path)
	if err != nil {
		return File{}, err
	}
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	reader, err := os.Open(abs)
	if err != nil {
		return File{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxPreviewBytes+1))
	if err != nil {
		return File{}, err
	}
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	truncated := len(data) > maxPreviewBytes
	if truncated {
		data = data[:maxPreviewBytes]
	}
	mimeType := detectMIME(clean, data)
	return File{
		Path: clean, Data: data, Binary: isBinary(data),
		Image: strings.HasPrefix(mimeType, "image/"), MIME: mimeType, Truncated: truncated,
	}, nil
}

// Status returns porcelain status split into staging-oriented groups.
func (c *Client) Status(ctx context.Context) (Status, error) {
	// Status is invoked in response to index watcher events. Prevent Git's
	// optional stat-cache refresh from rewriting the index and feeding the
	// resulting event back into another status scan.
	out, err := c.runner.Run(ctx, "git", "--no-optional-locks", "-C", c.root,
		"status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out)
}

const maxHistoryCommits = 200

// History returns commits reachable from all local and remote branches in
// topological order, with branch and tag references attached to their target commits.
func (c *Client) History(ctx context.Context, limit int) ([]Commit, error) {
	if limit <= 0 || limit > maxHistoryCommits {
		limit = maxHistoryCommits
	}
	// Record separators make path names (which may contain spaces and newlines)
	// unambiguous without invoking Git once per commit.
	out, err := c.git(ctx, "log", "--all", "--topo-order", "--name-only", "-z", fmt.Sprintf("-n%d", limit),
		"--format=%x1e%H%x00%P%x00%s%x00%an%x00%ae%x00%aI%x00")
	if err != nil {
		return nil, err
	}
	commits, err := parseHistory(out)
	if err != nil {
		return nil, err
	}

	refsOut, err := c.git(ctx, "for-each-ref", "--format=%(objectname)%00%(*objectname)%00%(refname)%00%(HEAD)%00%(symref)",
		"refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	refs, err := parseHistoryRefs(refsOut)
	if err != nil {
		return nil, err
	}
	for i := range commits {
		commits[i].Refs = refs[commits[i].SHA]
	}
	return commits, nil
}

// AuthorIdentity returns the effective repository author configuration.
func (c *Client) AuthorIdentity(ctx context.Context) (name, email string, err error) {
	out, err := c.git(ctx, "config", "--get", "user.name")
	if err == nil {
		name = strings.TrimSpace(string(out))
	}
	out, emailErr := c.git(ctx, "config", "--get", "user.email")
	if emailErr == nil {
		email = strings.TrimSpace(string(out))
	}
	if name == "" && email == "" {
		return "", "", fmt.Errorf("Git author identity is not configured")
	}
	return name, email, nil
}

func (c *Client) ensureSafeForHistoryAction(ctx context.Context) error {
	if _, err := c.git(ctx, "rev-parse", "--verify", "-q", "MERGE_HEAD"); err == nil {
		return fmt.Errorf("a merge is already in progress")
	}
	if _, err := c.git(ctx, "rev-parse", "--verify", "-q", "CHERRY_PICK_HEAD"); err == nil {
		return fmt.Errorf("a cherry-pick is already in progress")
	}
	if _, err := c.git(ctx, "rev-parse", "--verify", "-q", "REVERT_HEAD"); err == nil {
		return fmt.Errorf("a revert is already in progress")
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if len(status.Staged)+len(status.Unstaged)+len(status.Untracked) != 0 {
		return fmt.Errorf("working tree has changes; commit, stash, or discard them before this operation")
	}
	return nil
}

func validSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, r := range sha {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
func (c *Client) historyAction(ctx context.Context, sha string, args ...string) error {
	if !validSHA(sha) {
		return fmt.Errorf("invalid commit SHA")
	}
	if err := c.ensureSafeForHistoryAction(ctx); err != nil {
		return err
	}
	_, err := c.git(ctx, args...)
	return err
}
func (c *Client) Checkout(ctx context.Context, sha string) error {
	return c.historyAction(ctx, sha, "checkout", "--detach", sha)
}
func (c *Client) CherryPick(ctx context.Context, sha string) error {
	return c.historyAction(ctx, sha, "cherry-pick", sha)
}
func (c *Client) ResetSoft(ctx context.Context, sha string) error {
	return c.historyAction(ctx, sha, "reset", "--soft", sha)
}
func (c *Client) ResetHard(ctx context.Context, sha string) error {
	return c.historyAction(ctx, sha, "reset", "--hard", sha)
}
func (c *Client) Revert(ctx context.Context, sha string) error {
	return c.historyAction(ctx, sha, "revert", "--no-edit", sha)
}

// CommitPaths returns the paths changed by commit. It uses NUL-delimited output
// so paths containing whitespace or newlines remain intact.
func (c *Client) CommitPaths(ctx context.Context, sha string) ([]string, error) {
	if strings.TrimSpace(sha) == "" {
		return nil, fmt.Errorf("commit SHA is required")
	}
	out, err := c.git(ctx, "show", "--format=", "--name-only", "-z", "--no-renames", sha)
	if err != nil {
		return nil, err
	}
	paths := splitNUL(out)
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			result = append(result, path)
		}
	}
	return result, nil
}

// Branches lists local and remote branches without requiring a hosting CLI.
func (c *Client) Branches(ctx context.Context) ([]Branch, error) {
	out, err := c.git(ctx, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(authorname)%00%(authordate:iso-strict)%00%(HEAD)%00", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	// for-each-ref appends a newline after every formatted record. Remove it
	// after the trailing NUL delimiter so record fields remain aligned.
	fields := splitNUL(bytes.ReplaceAll(out, []byte{0, '\n'}, []byte{0}))
	if len(fields)%5 != 0 {
		return nil, fmt.Errorf("parse branch refs: expected fields in groups of 5, got %d", len(fields))
	}
	branches := make([]Branch, 0, len(fields)/5)
	for index := 0; index < len(fields); index += 5 {
		ref := fields[index]
		branch := Branch{SHA: fields[index+1], Author: fields[index+2], Head: fields[index+4] == "*"}
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			branch.Name = strings.TrimPrefix(ref, "refs/heads/")
		case strings.HasPrefix(ref, "refs/remotes/"):
			branch.Name = strings.TrimPrefix(ref, "refs/remotes/")
			branch.Remote = true
		default:
			continue
		}
		if fields[index+3] != "" {
			parsed, err := time.Parse(time.RFC3339, fields[index+3])
			if err != nil {
				return nil, fmt.Errorf("parse branch date %q: %w", fields[index+3], err)
			}
			branch.UpdatedAt = parsed
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

// Stage adds a path to the index.
func (c *Client) Stage(ctx context.Context, path string) error {
	clean, err := cleanPath(path)
	if err != nil {
		return err
	}
	_, err = c.git(ctx, "add", "--", clean)
	return err
}

// StageAll adds every working-tree change to the index.
func (c *Client) StageAll(ctx context.Context) error {
	_, err := c.git(ctx, "add", "-A", "--", ".")
	return err
}

// Unstage resets a path in the index to HEAD.
func (c *Client) Unstage(ctx context.Context, path string) error {
	clean, err := cleanPath(path)
	if err != nil {
		return err
	}
	if _, err := c.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil {
		_, err = c.git(ctx, "rm", "--cached", "-q", "-f", "--", clean)
		return err
	}
	paths := []string{clean}
	status, statusErr := c.Status(ctx)
	if statusErr != nil {
		return statusErr
	}
	for _, change := range status.Staged {
		if change.Path == clean && change.OldPath != "" {
			paths = append([]string{change.OldPath}, paths...)
			break
		}
	}
	args := append([]string{"reset", "-q", "HEAD", "--"}, paths...)
	_, err = c.git(ctx, args...)
	return err
}

// UnstageAll removes every staged change from the index.
func (c *Client) UnstageAll(ctx context.Context) error {
	if _, err := c.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil {
		_, err = c.git(ctx, "rm", "--cached", "-r", "-q", "-f", "--", ".")
		return err
	}
	_, err := c.git(ctx, "reset", "-q", "HEAD", "--", ".")
	return err
}

// Commit creates a commit from the current index using message.
func (c *Client) Commit(ctx context.Context, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("commit message cannot be empty")
	}
	_, err := c.git(ctx, "commit", "-m", message)
	return err
}

// Diff returns old/new contents and patch text. staged selects HEAD-to-index;
// otherwise it selects index-to-working-tree.
func (c *Client) Diff(ctx context.Context, path string, staged bool) (Diff, error) {
	clean, err := cleanPath(path)
	if err != nil {
		return Diff{}, err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return Diff{}, err
	}
	change, ok := statusChange(status, clean, staged)
	if !ok {
		return Diff{}, fmt.Errorf("%s is no longer changed", clean)
	}
	if change.Unmerged {
		return Diff{}, fmt.Errorf("%s has unresolved merge conflicts", clean)
	}
	renamedFrom := change.OldPath
	var old, new []byte
	if staged {
		if change.Code != 'A' || renamedFrom != "" {
			oldPath := clean
			if renamedFrom != "" {
				oldPath = renamedFrom
			}
			old, err = c.gitFile(ctx, "HEAD:"+oldPath)
			if err != nil {
				return Diff{}, err
			}
		}
		if change.Code != 'D' {
			new, err = c.gitFile(ctx, ":"+clean)
			if err != nil {
				return Diff{}, err
			}
		}
	} else {
		if change.Code != '?' {
			oldPath := clean
			if renamedFrom != "" {
				oldPath = renamedFrom
			}
			old, err = c.gitFile(ctx, ":"+oldPath)
			if err != nil {
				return Diff{}, err
			}
		}
		if change.Code != 'D' {
			abs, _, resolveErr := c.resolve(clean)
			if resolveErr != nil {
				return Diff{}, resolveErr
			}
			new, err = os.ReadFile(abs)
			if err != nil {
				return Diff{}, err
			}
			if len(new) > maxPreviewBytes {
				return Diff{}, fmt.Errorf("%s exceeds the 8 MiB diff preview limit", clean)
			}
		}
	}
	args := []string{"diff", "--binary"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	if renamedFrom != "" {
		args = append(args, renamedFrom)
	}
	args = append(args, clean)
	patch, err := c.git(ctx, args...)
	if err != nil {
		return Diff{}, err
	}
	if len(patch) == 0 && !staged && len(old) == 0 && !isBinary(new) {
		patch = untrackedPatch(clean, new)
	}
	return Diff{Path: clean, Old: old, New: new, Binary: isBinary(old) || isBinary(new), Patch: string(patch)}, nil
}

func statusChange(status Status, path string, staged bool) (Change, bool) {
	groups := [][]Change{status.Unstaged, status.Untracked}
	if staged {
		groups = [][]Change{status.Staged}
	}
	for _, group := range groups {
		for _, change := range group {
			if change.Path == path {
				return change, true
			}
		}
	}
	return Change{}, false
}

func (c *Client) git(ctx context.Context, args ...string) ([]byte, error) {
	base := []string{"-C", c.root}
	return c.runner.Run(ctx, "git", append(base, args...)...)
}

func (c *Client) gitFile(ctx context.Context, object string) ([]byte, error) {
	data, err := c.git(ctx, "show", "--no-textconv", object)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPreviewBytes {
		return nil, fmt.Errorf("Git object exceeds the 8 MiB diff preview limit")
	}
	return data, nil
}

func (c *Client) resolve(path string) (string, string, error) {
	clean, err := cleanPath(path)
	if err != nil {
		return "", "", err
	}
	abs := filepath.Join(c.root, filepath.FromSlash(clean))
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", "", err
	}
	if err := c.ensureInside(path, parent); err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		if err := c.ensureInside(path, resolved); err != nil {
			return "", "", err
		}
		return resolved, clean, nil
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	return abs, clean, nil
}

func (c *Client) ensureInside(original, resolved string) error {
	rel, err := filepath.Rel(c.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes worktree", original)
	}
	return nil
}

func cleanPath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid worktree path %q", path)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid worktree path %q", path)
	}
	return clean, nil
}

func splitNUL(data []byte) []string {
	parts := bytes.Split(data, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			result = append(result, string(part))
		}
	}
	return result
}

func parseStatus(data []byte) (Status, error) {
	fields := splitNUL(data)
	var status Status
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 3 || field[2] != ' ' {
			return Status{}, fmt.Errorf("invalid porcelain status entry %q", field)
		}
		x, y, path := field[0], field[1], field[3:]
		unmerged := x == 'U' || y == 'U' || x == 'A' && y == 'A' || x == 'D' && y == 'D'
		oldPath := ""
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i >= len(fields) {
				return Status{}, fmt.Errorf("missing original path for %q", path)
			}
			oldPath = fields[i]
		}
		if x == '?' && y == '?' {
			status.Untracked = append(status.Untracked, Change{Path: path, Code: '?'})
			continue
		}
		if x != ' ' {
			status.Staged = append(status.Staged, Change{Path: path, OldPath: oldPath, Code: x, Unmerged: unmerged})
		}
		if y != ' ' {
			status.Unstaged = append(status.Unstaged, Change{Path: path, OldPath: oldPath, Code: y, Unmerged: unmerged})
		}
	}
	return status, nil
}

func parseHistory(data []byte) ([]Commit, error) {
	if len(data) == 0 {
		return nil, nil
	}
	// Retain support for the original five-field stream. It is useful for
	// lightweight runners and makes the parser tolerant of older Git fixtures.
	if !bytes.Contains(data, []byte{0x1e}) {
		fields := bytes.Split(data, []byte{0})
		if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
			fields = fields[:len(fields)-1]
		}
		if len(fields)%5 != 0 {
			return nil, fmt.Errorf("invalid Git history: got %d fields", len(fields))
		}
		commits := make([]Commit, 0, len(fields)/5)
		for i := 0; i < len(fields); i += 5 {
			at, err := time.Parse(time.RFC3339, string(fields[i+4]))
			if err != nil {
				return nil, fmt.Errorf("parse authored time for %s: %w", fields[i], err)
			}
			commits = append(commits, Commit{SHA: string(fields[i]), Parents: strings.Fields(string(fields[i+1])), Subject: string(fields[i+2]), Author: string(fields[i+3]), AuthoredAt: at})
		}
		return commits, nil
	}
	records := bytes.Split(data, []byte{0x1e})
	commits := make([]Commit, 0, len(records))
	for _, record := range records[1:] {
		fields := bytes.Split(record, []byte{0})
		if len(fields) < 6 {
			return nil, fmt.Errorf("invalid Git history record")
		}
		sha := string(fields[0])
		if sha == "" {
			return nil, fmt.Errorf("invalid Git history: empty commit SHA")
		}
		authoredAt, err := time.Parse(time.RFC3339, string(fields[5]))
		if err != nil {
			return nil, fmt.Errorf("parse authored time for %s: %w", sha, err)
		}
		var parents []string
		if len(fields[1]) > 0 {
			parents = strings.Fields(string(fields[1]))
		}
		paths := make([]string, 0, len(fields)-6)
		for _, path := range fields[6:] {
			if len(path) > 0 {
				paths = append(paths, string(path))
			}
		}
		commits = append(commits, Commit{
			SHA: sha, Parents: parents, Subject: string(fields[2]),
			Author: string(fields[3]), AuthorEmail: string(fields[4]), Paths: paths, AuthoredAt: authoredAt,
		})
	}
	return commits, nil
}

func parseHistoryRefs(data []byte) (map[string][]Ref, error) {
	refs := make(map[string][]Ref)
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		fields := bytes.Split(line, []byte{0})
		if len(fields) != 5 || len(fields[0]) == 0 {
			return nil, fmt.Errorf("invalid Git ref %q", line)
		}
		if len(fields[4]) > 0 {
			continue
		}
		fullName := string(fields[2])
		ref := Ref{Head: string(fields[3]) == "*"}
		switch {
		case strings.HasPrefix(fullName, "refs/heads/"):
			ref.Name = strings.TrimPrefix(fullName, "refs/heads/")
		case strings.HasPrefix(fullName, "refs/remotes/"):
			ref.Name = strings.TrimPrefix(fullName, "refs/remotes/")
			ref.Remote = true
		case strings.HasPrefix(fullName, "refs/tags/"):
			ref.Name = strings.TrimPrefix(fullName, "refs/tags/")
			ref.Tag = true
		default:
			return nil, fmt.Errorf("unexpected Git ref %q", fullName)
		}
		if ref.Name == "" {
			return nil, fmt.Errorf("invalid Git ref %q", fullName)
		}
		sha := string(fields[0])
		if ref.Tag && len(fields[1]) > 0 {
			sha = string(fields[1])
		}
		refs[sha] = append(refs[sha], ref)
	}
	return refs, nil
}

func detectMIME(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return "text/markdown"
	}
	if extType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); extType != "" {
		return strings.Split(extType, ";")[0]
	}
	return strings.Split(http.DetectContentType(data), ";")[0]
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

func untrackedPatch(path string, data []byte) []byte {
	var patch strings.Builder
	fmt.Fprintf(&patch, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", path, path, path)
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []byte(patch.String())
	}
	fmt.Fprintf(&patch, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		patch.WriteByte('+')
		patch.WriteString(line)
		patch.WriteByte('\n')
	}
	return []byte(patch.String())
}
