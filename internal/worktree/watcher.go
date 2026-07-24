package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchUpdate reports either a relevant filesystem change or a watcher error.
type WatchUpdate struct {
	Path string
	Err  error
}

// Watcher watches one worktree and the Git metadata that affects its status.
type Watcher struct {
	fs      *fsnotify.Watcher
	root    string
	gitDirs []string
	polling bool
	updates chan WatchUpdate
	done    chan struct{}
	once    sync.Once
}

// NewWatcher starts a recursive watcher. Symlinked directories are deliberately
// not followed, and Git object storage is excluded from the watch set.
func NewWatcher(root string) (*Watcher, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(root); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("watch root %q is not a directory", root)
	}
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return newPollingWatcher(root), nil
	}
	w := &Watcher{fs: fs, root: filepath.Clean(root), updates: make(chan WatchUpdate, 32), done: make(chan struct{})}
	gitDirs, err := resolveGitDirs(root)
	if err != nil {
		fs.Close()
		return nil, err
	}
	w.gitDirs = gitDirs
	if err := w.addTree(root, true); err != nil {
		fs.Close()
		return newPollingWatcher(root), nil
	}
	for _, dir := range gitDirs {
		if err := w.addGitWatches(dir); err != nil {
			fs.Close()
			return newPollingWatcher(root), nil
		}
	}
	go w.run()
	return w, nil
}

// newPollingWatcher is a portable fallback for directory trees that exceed
// the operating system's per-directory watch limit.
func newPollingWatcher(root string) *Watcher {
	w := &Watcher{root: filepath.Clean(root), polling: true, updates: make(chan WatchUpdate, 32), done: make(chan struct{})}
	initial, initialErr := snapshotTree(w.root)
	go w.poll(initial, initialErr)
	return w
}

func resolveGitDirs(root string) ([]string, error) {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		if os.IsNotExist(err) {
			// A plain directory is still a valid Files-only workspace. It has no
			// Git metadata to watch, but its filesystem tree remains watchable.
			return nil, nil
		}
		return nil, fmt.Errorf("resolve Git directory: %w", err)
	}
	gitDir := dotGit
	if !info.IsDir() {
		data, readErr := os.ReadFile(dotGit)
		if readErr != nil {
			return nil, fmt.Errorf("read .git file: %w", readErr)
		}
		value := strings.TrimSpace(string(data))
		if !strings.HasPrefix(value, "gitdir:") {
			return nil, fmt.Errorf("invalid .git file")
		}
		gitDir = strings.TrimSpace(strings.TrimPrefix(value, "gitdir:"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
	}
	gitDir, err = filepath.Abs(gitDir)
	if err != nil {
		return nil, err
	}
	dirs := []string{filepath.Clean(gitDir)}
	if data, readErr := os.ReadFile(filepath.Join(gitDir, "commondir")); readErr == nil {
		common := strings.TrimSpace(string(data))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		common, _ = filepath.Abs(common)
		if common != "" && filepath.Clean(common) != dirs[0] {
			dirs = append(dirs, filepath.Clean(common))
		}
	}
	return dirs, nil
}

func (w *Watcher) addTree(root string, skipDotGit bool) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		if skipDotGit && path != root && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		return w.fs.Add(path)
	})
}

func (w *Watcher) addGitWatches(gitDir string) error {
	if err := w.fs.Add(gitDir); err != nil {
		return err
	}
	refs := filepath.Join(gitDir, "refs")
	if info, err := os.Stat(refs); err == nil && info.IsDir() {
		return w.addTree(refs, false)
	}
	return nil
}

func (w *Watcher) Updates() <-chan WatchUpdate { return w.updates }

func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		if w.fs != nil {
			err = w.fs.Close()
		}
	})
	return err
}

type fileStamp struct {
	modTime time.Time
	size    int64
	mode    os.FileMode
}

func (w *Watcher) poll(previous map[string]fileStamp, initialErr error) {
	defer close(w.updates)
	if initialErr != nil {
		w.send(WatchUpdate{Err: initialErr})
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			current, err := snapshotTree(w.root)
			if err != nil {
				w.send(WatchUpdate{Err: err})
				continue
			}
			if !sameSnapshot(previous, current) {
				previous = current
				w.send(WatchUpdate{Path: w.root})
			}
		}
	}
}

func snapshotTree(root string) (map[string]fileStamp, error) {
	snapshot := make(map[string]fileStamp)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root && entry.IsDir() && (entry.Name() == ".git" || entry.Type()&os.ModeSymlink != 0) {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		snapshot[path] = fileStamp{modTime: info.ModTime(), size: info.Size(), mode: info.Mode()}
		return nil
	})
	return snapshot, err
}

func sameSnapshot(left, right map[string]fileStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for path, stamp := range left {
		if right[path] != stamp {
			return false
		}
	}
	return true
}

func (w *Watcher) run() {
	defer close(w.updates)
	for {
		select {
		case <-w.done:
			return
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			w.send(WatchUpdate{Err: err})
		case event, ok := <-w.fs.Events:
			if !ok {
				return
			}
			// kqueue may report chmod/attribute notifications when Git only
			// reads the index. They do not represent workspace content changes
			// and feeding them back into Status creates a refresh loop.
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
					if w.insideRoot(event.Name) {
						if err := w.addTree(event.Name, true); err != nil {
							w.send(WatchUpdate{Err: err})
						}
					} else if w.insideGitRefs(event.Name) {
						if err := w.addTree(event.Name, false); err != nil {
							w.send(WatchUpdate{Err: err})
						}
					}
				}
			}
			if w.relevant(event.Name) {
				w.send(WatchUpdate{Path: event.Name})
			}
		}
	}
}

func (w *Watcher) insideRoot(path string) bool { return within(w.root, path) }

func (w *Watcher) insideGitRefs(path string) bool {
	for _, dir := range w.gitDirs {
		if within(filepath.Join(dir, "refs"), path) {
			return true
		}
	}
	return false
}

func (w *Watcher) relevant(path string) bool {
	if w.insideRoot(path) && filepath.Base(path) != ".git" {
		return true
	}
	for _, dir := range w.gitDirs {
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		first := strings.Split(rel, string(filepath.Separator))[0]
		if first == "index" || first == "HEAD" || first == "packed-refs" || first == "refs" {
			return true
		}
	}
	return false
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (w *Watcher) send(update WatchUpdate) {
	select {
	case w.updates <- update:
	case <-w.done:
	default:
		// A pending event is enough: the model debounces and reloads the full view.
	}
}
