package worktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jwmtp2/gtui/internal/provider"
)

type failingIgnoreRunner struct{}

func (failingIgnoreRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("git unavailable")
}

func (failingIgnoreRunner) RunInput(context.Context, []byte, string, ...string) ([]byte, error) {
	return nil, errors.New("ignore check failed")
}

func (failingIgnoreRunner) LookPath(string) error { return nil }

func TestEntriesAndRead(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "README.md", []byte("hello\n"))
	writeFile(t, repo, "docs/guide.txt", []byte("guide\n"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	writeFile(t, repo, "assets/icon.png", []byte("\x89PNG\r\n\x1a\n\x00payload"))
	writeFile(t, repo, "docs/draft.txt", []byte("draft\n"))
	writeFile(t, repo, "ignored/secret.txt", []byte("secret\n"))
	writeFile(t, repo, ".gitignore", []byte("ignored/\n"))

	client := New(repo, provider.ExecRunner{})
	entries, err := client.Entries(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i] = entry.Path
	}
	for _, want := range []string{".gitignore", "README.md", "assets", "docs"} {
		if !slices.Contains(paths, want) {
			t.Errorf("Entries() missing %q: %v", want, paths)
		}
	}
	for _, unwanted := range []string{".git", "assets/icon.png", "docs/draft.txt", "docs/guide.txt", "ignored"} {
		if slices.Contains(paths, unwanted) {
			t.Errorf("Entries() unexpectedly contains %q: %v", unwanted, paths)
		}
	}
	if entry := entries[slices.Index(paths, "docs")]; !entry.IsDir || entry.Name != "docs" {
		t.Errorf("directory entry = %#v", entry)
	}

	docs, err := client.Entries(context.Background(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	docPaths := make([]string, len(docs))
	for i, entry := range docs {
		docPaths[i] = entry.Path
	}
	if want := []string{"docs/draft.txt", "docs/guide.txt"}; !slices.Equal(docPaths, want) {
		t.Errorf("Entries(docs) = %v, want %v", docPaths, want)
	}

	text, err := client.Read(context.Background(), "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if text.Binary || text.Image || string(text.Data) != "hello\n" || text.MIME != "text/markdown" {
		t.Errorf("text File = %#v", text)
	}
	image, err := client.Read(context.Background(), "assets/icon.png")
	if err != nil {
		t.Fatal(err)
	}
	if !image.Binary || !image.Image || image.MIME != "image/png" {
		t.Errorf("image File = %#v", image)
	}
}

func TestEntriesPropagatesIgnoreCheckFailure(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "visible.txt", []byte("visible"))
	client := New(root, failingIgnoreRunner{})
	if _, err := client.Entries(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "ignore check failed") {
		t.Fatalf("Entries ignore failure = %v", err)
	}
}

func TestNewResolvesRepositoryRootFromSubdirectory(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "root.txt", []byte("root\n"))
	writeFile(t, repo, "nested/child.txt", []byte("child\n"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")

	client := New(filepath.Join(repo, "nested"), provider.ExecRunner{})
	entries, err := client.Entries(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	if !slices.Contains(paths, "root.txt") || !slices.Contains(paths, "nested") || slices.Contains(paths, "nested/child.txt") {
		t.Fatalf("subdirectory client entries = %v", paths)
	}
}

func TestStatusStageUnstageAndDiff(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "both.txt", []byte("base\n"))
	writeFile(t, repo, "old name.txt", []byte("rename me\n"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")

	writeFile(t, repo, "both.txt", []byte("indexed\n"))
	git(t, repo, "add", "--", "both.txt")
	writeFile(t, repo, "both.txt", []byte("working\n"))
	git(t, repo, "mv", "old name.txt", "new name.txt")
	writeFile(t, repo, "--odd name.txt", []byte("untracked\n"))

	client := New(repo, provider.ExecRunner{})
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertChange(t, status.Staged, "both.txt", "", 'M')
	assertChange(t, status.Unstaged, "both.txt", "", 'M')
	assertChange(t, status.Staged, "new name.txt", "old name.txt", 'R')
	assertChange(t, status.Untracked, "--odd name.txt", "", '?')
	renamed, err := client.Diff(context.Background(), "new name.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(renamed.Old) != "rename me\n" || string(renamed.New) != "rename me\n" || !strings.Contains(renamed.Patch, "rename from old name.txt") {
		t.Errorf("renamed Diff = %#v", renamed)
	}

	staged, err := client.Diff(context.Background(), "both.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(staged.Old) != "base\n" || string(staged.New) != "indexed\n" || !strings.Contains(staged.Patch, "-base") || !strings.Contains(staged.Patch, "+indexed") {
		t.Errorf("staged Diff = %#v", staged)
	}
	working, err := client.Diff(context.Background(), "both.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(working.Old) != "indexed\n" || string(working.New) != "working\n" || !strings.Contains(working.Patch, "-indexed") || !strings.Contains(working.Patch, "+working") {
		t.Errorf("working Diff = %#v", working)
	}

	if err := client.Stage(context.Background(), "--odd name.txt"); err != nil {
		t.Fatal(err)
	}
	stagedNew, err := client.Diff(context.Background(), "--odd name.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(stagedNew.Old) != 0 || string(stagedNew.New) != "untracked\n" || !strings.Contains(stagedNew.Patch, "new file mode") {
		t.Errorf("new staged Diff = %#v", stagedNew)
	}
	if err := client.Unstage(context.Background(), "--odd name.txt"); err != nil {
		t.Fatal(err)
	}
	status, err = client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertChange(t, status.Untracked, "--odd name.txt", "", '?')

	untracked, err := client.Diff(context.Background(), "--odd name.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(untracked.Old) != 0 || string(untracked.New) != "untracked\n" || !strings.Contains(untracked.Patch, "+untracked") {
		t.Errorf("untracked Diff = %#v", untracked)
	}
}

func TestRejectsPathsOutsideWorktree(t *testing.T) {
	repo := newRepo(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "linked-secret.txt")); err != nil {
		t.Fatal(err)
	}
	client := New(repo, provider.ExecRunner{})
	for _, path := range []string{"../secret.txt", outside, "escape/secret.txt", "linked-secret.txt"} {
		if _, err := client.Read(context.Background(), path); err == nil {
			t.Errorf("Read(%q) unexpectedly succeeded", path)
		}
		if err := client.Stage(context.Background(), path); err == nil && (path == "../secret.txt" || path == outside) {
			t.Errorf("Stage(%q) unexpectedly succeeded", path)
		}
		if _, err := client.Entries(context.Background(), path); err == nil {
			t.Errorf("Entries(%q) unexpectedly succeeded", path)
		}
	}
}

func TestUnstageBeforeFirstCommit(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "first.txt", []byte("first\n"))
	client := New(repo, provider.ExecRunner{})
	if err := client.Stage(context.Background(), "first.txt"); err != nil {
		t.Fatal(err)
	}
	if err := client.Unstage(context.Background(), "first.txt"); err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertChange(t, status.Untracked, "first.txt", "", '?')
	if len(status.Staged) != 0 {
		t.Fatalf("staged after Unstage = %#v", status.Staged)
	}
}

func TestUnstageRenameClearsBothIndexPaths(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "old.txt", []byte("content\n"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	git(t, repo, "mv", "old.txt", "new.txt")

	client := New(repo, provider.ExecRunner{})
	if err := client.Unstage(context.Background(), "new.txt"); err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Staged) != 0 {
		t.Fatalf("staged changes after rename unstage = %#v", status.Staged)
	}
	assertChange(t, status.Unstaged, "old.txt", "", 'D')
	assertChange(t, status.Untracked, "new.txt", "", '?')
}

func TestCommitCreatesCommitFromIndex(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "main.go", []byte("package main\n"))
	client := New(repo, provider.ExecRunner{})
	if err := client.Stage(context.Background(), "main.go"); err != nil {
		t.Fatal(err)
	}
	if err := client.Commit(context.Background(), "initial implementation"); err != nil {
		t.Fatal(err)
	}
	message := strings.TrimSpace(string(git(t, repo, "log", "-1", "--pretty=%B")))
	if message != "initial implementation" {
		t.Fatalf("commit message = %q", message)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Staged) != 0 {
		t.Fatalf("staged changes after commit = %#v", status.Staged)
	}
	if err := client.Commit(context.Background(), "  "); err == nil {
		t.Fatal("empty commit message unexpectedly succeeded")
	}
}

func TestBinaryDetection(t *testing.T) {
	if isBinary([]byte("plain text\n")) {
		t.Fatal("plain text detected as binary")
	}
	if !isBinary([]byte{'a', 0, 'b'}) {
		t.Fatal("NUL-containing data not detected as binary")
	}
	if got := detectMIME("drawing.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)); got != "image/svg+xml" {
		t.Fatalf("detectMIME(svg) = %q", got)
	}
}

func TestReadHonorsContextAndPreviewLimit(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "large.txt", bytes.Repeat([]byte{'x'}, maxPreviewBytes+1))
	client := New(repo, provider.ExecRunner{})

	file, err := client.Read(context.Background(), "large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !file.Truncated || len(file.Data) != maxPreviewBytes {
		t.Fatalf("large file = truncated:%v bytes:%d", file.Truncated, len(file.Data))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Read(ctx, "large.txt"); err == nil {
		t.Fatal("cancelled Read unexpectedly succeeded")
	}
	if _, err := client.Entries(ctx, ""); err == nil {
		t.Fatal("cancelled Entries unexpectedly succeeded")
	}
}

func TestDiffReportsUnmergedConflict(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "conflict.txt", []byte("base\n"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "base")
	baseBranch := strings.TrimSpace(string(git(t, repo, "branch", "--show-current")))
	git(t, repo, "checkout", "-q", "-b", "other")
	writeFile(t, repo, "conflict.txt", []byte("other\n"))
	git(t, repo, "commit", "-am", "other")
	git(t, repo, "checkout", "-q", baseBranch)
	writeFile(t, repo, "conflict.txt", []byte("current\n"))
	git(t, repo, "commit", "-am", "current")
	cmd := exec.Command("git", "-C", repo, "merge", "other")
	if err := cmd.Run(); err == nil {
		t.Fatal("merge unexpectedly succeeded")
	}

	client := New(repo, provider.ExecRunner{})
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Staged) == 0 || !status.Staged[0].Unmerged {
		t.Fatalf("conflict status = %#v", status)
	}
	if _, err := client.Diff(context.Background(), "conflict.txt", false); err == nil || !strings.Contains(err.Error(), "unresolved merge conflicts") {
		t.Fatalf("conflict Diff error = %v", err)
	}
}

func assertChange(t *testing.T, changes []Change, path, oldPath string, code byte) {
	t.Helper()
	for _, change := range changes {
		if change.Path == path && change.OldPath == oldPath && change.Code == code {
			return
		}
	}
	t.Errorf("missing change {%q %q %q} in %#v", path, oldPath, code, changes)
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.com")
	return repo
}

func writeFile(t *testing.T, repo, path string, data []byte) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, bytes.TrimSpace(out))
	}
	return out
}
