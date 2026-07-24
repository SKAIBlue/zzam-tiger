package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
)

func TestWatcherDetectsWorktreeChangesAndNewDirectories(t *testing.T) {
	root := initWatcherRepo(t)
	w, err := NewWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	dir := filepath.Join(root, "new-dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	waitForWatchPath(t, w, dir)
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchPath(t, w, file)
	if err := os.WriteFile(file, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchPath(t, w, file)
	renamed := filepath.Join(dir, "renamed.txt")
	if err := os.Rename(file, renamed); err != nil {
		t.Fatal(err)
	}
	waitForAnyWatchPath(t, w, file, renamed)
	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	waitForWatchUpdate(t, w)
}

func TestWatcherDetectsGitIndexHeadAndRefs(t *testing.T) {
	root := initWatcherRepo(t)
	w, err := NewWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	file := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(file, []byte("change"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	waitForGitWatchPath(t, w, "index")
	runGit(t, root, "commit", "-m", "change")
	waitForGitWatchPath(t, w, "HEAD", "refs")
	runGit(t, root, "branch", "topic")
	waitForGitWatchPath(t, w, "refs")
	runGit(t, root, "checkout", "-b", "checked-out")
	waitForGitWatchPath(t, w, "HEAD", "refs")
	runGit(t, root, "reset", "--mixed", "HEAD~1")
	waitForGitWatchPath(t, w, "index")
}

func TestWatcherDoesNotFollowSymlinkedDirectories(t *testing.T) {
	root := initWatcherRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case update := <-w.Updates():
			if within(outside, update.Path) {
				t.Fatalf("outside symlink target produced watcher update: %#v", update)
			}
		case <-deadline:
			break drain
		}
	}
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchPath(t, w, inside)
}

func TestWatcherStartupErrorIsReturned(t *testing.T) {
	if _, err := NewWatcher(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("NewWatcher succeeded without a worktree")
	}
}

func TestStatusDoesNotTriggerWatcher(t *testing.T) {
	root := initWatcherRepo(t)
	client := New(root, provider.ExecRunner{})
	w, err := NewWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Ignore any backend event delivered while the initial watch set settles.
	drainWatcher(w, 150*time.Millisecond)
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-w.Updates():
		t.Fatalf("read-only status triggered watcher update: %#v", update)
	case <-time.After(300 * time.Millisecond):
	}
}

func drainWatcher(w *Watcher, quiet time.Duration) {
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case <-w.Updates():
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(quiet)
		case <-timer.C:
			return
		}
	}
}

func TestWatcherSupportsLinkedWorktreeAndCloses(t *testing.T) {
	root := initWatcherRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, root, "worktree", "add", "-b", "linked-test", linked)
	w, err := NewWatcher(linked)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "linked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchPath(t, w, filepath.Join(linked, "linked.txt"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-w.Updates():
			if !ok {
				return
			}
			// Events queued before Close remain readable from a closed buffered
			// channel. Keep draining until the channel reaches its closed state.
		case <-deadline:
			t.Fatal("watcher updates channel did not close")
		}
	}
}

func initWatcherRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "watcher@example.com")
	runGit(t, root, "config", "user.name", "Watcher Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")
	return root
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func waitForWatchPath(t *testing.T, w *Watcher, path string) {
	t.Helper()
	waitForAnyWatchPath(t, w, path)
}

func waitForAnyWatchPath(t *testing.T, w *Watcher, paths ...string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case update, ok := <-w.Updates():
			if !ok {
				t.Fatal("watcher closed unexpectedly")
			}
			if update.Err != nil {
				t.Fatal(update.Err)
			}
			for _, path := range paths {
				if filepath.Clean(update.Path) == filepath.Clean(path) {
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for watcher path %v", paths)
		}
	}
}

func waitForGitWatchPath(t *testing.T, w *Watcher, parts ...string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case update := <-w.Updates():
			if update.Err != nil {
				t.Fatal(update.Err)
			}
			for _, part := range parts {
				if filepath.Base(update.Path) == part || containsPathPart(update.Path, part) {
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for Git watcher path containing %v", parts)
		}
	}
}

func waitForWatchUpdate(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case update := <-w.Updates():
		if update.Err != nil {
			t.Fatal(update.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watcher update")
	}
}

func containsPathPart(path, part string) bool {
	for _, value := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if value == part {
			return true
		}
	}
	return false
}
