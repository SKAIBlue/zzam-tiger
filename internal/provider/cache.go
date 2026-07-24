package provider

// CacheProvider adds a small, private, cache-first layer for remote data that
// is expensive to obtain repeatedly.  It deliberately caches only GitHub
// Issues and CI details: other resources retain their existing semantics.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cacheSchema = 1

type cacheEnvelope struct {
	Schema    int             `json:"schema"`
	Key       string          `json:"key"`
	UpdatedAt time.Time       `json:"updated_at"`
	Payload   json.RawMessage `json:"payload"`
}

// CachedProvider returns a provider whose eligible reads are served from an
// on-disk cache before a deduplicated refresh is started. Cache files are
// private (0700 directory, 0600 files) and atomically replaced.
func CachedProvider(inner Provider) Provider {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	if override := strings.TrimSpace(os.Getenv("ZZAM_TIGER_CACHE_DIR")); override != "" {
		dir = override
	}
	return &cachedProvider{Provider: inner, dir: filepath.Join(dir, "zzam-tiger", "v1"), refreshing: make(map[string]bool)}
}

type cachedProvider struct {
	Provider
	dir        string
	mu         sync.Mutex
	refreshing map[string]bool
}

func (p *cachedProvider) eligible(kind Kind) bool {
	return kind == CIRuns || kind == Issues && p.Name() == "GitHub"
}

func (p *cachedProvider) cacheKey(kind Kind, filter string, item Item) string {
	// Repository plus provider name separate normal providers. Hosts are part of
	// the identity when supplied by the concrete provider.
	host := ""
	if value, ok := p.Provider.(interface{ cacheHost() string }); ok {
		host = value.cacheHost()
	}
	return fmt.Sprintf("v%d|%s|%s|%s|%d|%s|%s", cacheSchema, p.Name(), host, p.Repository(), kind, filter, item.ID)
}

func (p *cachedProvider) file(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(p.dir, hex.EncodeToString(sum[:])+".json")
}

func (p *cachedProvider) read(key string, out any) bool {
	data, err := os.ReadFile(p.file(key))
	if err != nil {
		return false
	}
	var entry cacheEnvelope
	if json.Unmarshal(data, &entry) != nil || entry.Schema != cacheSchema || entry.Key != key || len(entry.Payload) == 0 {
		return false
	}
	return json.Unmarshal(entry.Payload, out) == nil
}

func (p *cachedProvider) write(key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data, err := json.Marshal(cacheEnvelope{Schema: cacheSchema, Key: key, UpdatedAt: time.Now().UTC(), Payload: payload})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(p.dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.dir, ".cache-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, p.file(key))
}

func (p *cachedProvider) refresh(key string, run func(context.Context) error) {
	p.mu.Lock()
	if p.refreshing[key] {
		p.mu.Unlock()
		return
	}
	p.refreshing[key] = true
	p.mu.Unlock()
	go func() {
		defer func() { p.mu.Lock(); delete(p.refreshing, key); p.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		_ = run(ctx)
	}()
}

func (p *cachedProvider) List(ctx context.Context, kind Kind, filter Filter) ([]Item, error) {
	if !p.eligible(kind) {
		return p.Provider.List(ctx, kind, filter)
	}
	key := p.cacheKey(kind, filter.Value, Item{})
	var cached []Item
	if p.read(key, &cached) {
		p.refresh(key, func(ctx context.Context) error {
			items, err := p.Provider.List(ctx, kind, filter)
			if err == nil {
				err = p.write(key, items)
			}
			return err
		})
		return cached, nil
	}
	items, err := p.Provider.List(ctx, kind, filter)
	if err == nil {
		_ = p.write(key, items)
	}
	return items, err
}

func (p *cachedProvider) Detail(ctx context.Context, kind Kind, item Item) (Detail, error) {
	if !p.eligible(kind) {
		return p.Provider.Detail(ctx, kind, item)
	}
	key := p.cacheKey(kind, "detail", item)
	var cached Detail
	if p.read(key, &cached) {
		p.refresh(key, func(ctx context.Context) error {
			detail, err := p.Provider.Detail(ctx, kind, item)
			if err == nil {
				err = p.write(key, detail)
			}
			return err
		})
		return cached, nil
	}
	detail, err := p.Provider.Detail(ctx, kind, item)
	if err == nil {
		_ = p.write(key, detail)
	}
	return detail, err
}

func (p *cachedProvider) invalidateIssues() {
	// Cache keys are hashed, so remove only this provider/repository directory's
	// entries by validating their embedded identities before unlinking.
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(p.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var env cacheEnvelope
		if json.Unmarshal(data, &env) == nil && strings.Contains(env.Key, "|"+p.Repository()+"|"+fmt.Sprint(Issues)+"|") {
			_ = os.Remove(path)
		}
	}
}

func (p *cachedProvider) AddComment(ctx context.Context, kind Kind, item Item, body string) error {
	err := p.Provider.AddComment(ctx, kind, item, body)
	if err == nil && kind == Issues && p.Name() == "GitHub" {
		p.invalidateIssues()
	}
	return err
}
func (p *cachedProvider) SetIssueState(ctx context.Context, item Item, open bool) error {
	err := p.Provider.SetIssueState(ctx, item, open)
	if err == nil && p.Name() == "GitHub" {
		p.invalidateIssues()
	}
	return err
}
func (p *cachedProvider) SetAssigned(ctx context.Context, kind Kind, item Item, assigned bool) error {
	err := p.Provider.SetAssigned(ctx, kind, item, assigned)
	if err == nil && kind == Issues && p.Name() == "GitHub" {
		p.invalidateIssues()
	}
	return err
}
func (p *cachedProvider) SetIssueLabels(ctx context.Context, item Item, labels []string) error {
	err := p.Provider.SetIssueLabels(ctx, item, labels)
	if err == nil && p.Name() == "GitHub" {
		p.invalidateIssues()
	}
	return err
}
