package provider

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

type cacheTestProvider struct {
	Provider
	mu        sync.Mutex
	items     []Item
	detail    Detail
	listHit   int
	detailHit int
	gate      <-chan struct{}
}

func (p *cacheTestProvider) Name() string       { return "GitHub" }
func (p *cacheTestProvider) Repository() string { return "owner/repo" }
func (p *cacheTestProvider) cacheHost() string  { return "github.example" }
func (p *cacheTestProvider) List(_ context.Context, _ Kind, _ Filter) ([]Item, error) {
	if p.gate != nil {
		<-p.gate
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listHit++
	return append([]Item(nil), p.items...), nil
}
func (p *cacheTestProvider) Detail(_ context.Context, _ Kind, _ Item) (Detail, error) {
	if p.gate != nil {
		<-p.gate
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.detailHit++
	return p.detail, nil
}

func TestCachedProviderListUsesSnapshotBeforeRefresh(t *testing.T) {
	gate := make(chan struct{})
	inner := &cacheTestProvider{items: []Item{{ID: "remote", Title: "remote"}}, gate: gate}
	p := &cachedProvider{Provider: inner, dir: t.TempDir(), refreshing: make(map[string]bool)}
	key := p.cacheKey(Issues, "open", Item{})
	if err := p.write(key, []Item{{ID: "cached", Title: "cached"}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	items, err := p.List(context.Background(), Issues, Filter{Value: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "cached" {
		t.Fatalf("got %#v", items)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("cache read waited for refresh")
	}
	close(gate)
	deadline := time.After(time.Second)
	for {
		var got []Item
		if p.read(key, &got) && len(got) == 1 && got[0].ID == "remote" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("background refresh did not replace cache")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestCachedProviderRejectsMismatchedSnapshot(t *testing.T) {
	inner := &cacheTestProvider{detail: Detail{Item: Item{ID: "9", Title: "fresh"}}}
	p := &cachedProvider{Provider: inner, dir: t.TempDir(), refreshing: make(map[string]bool)}
	key := p.cacheKey(CIRuns, "detail", Item{ID: "9"})
	if err := p.write("wrong-key", Detail{Item: Item{ID: "9", Title: "wrong"}}); err != nil {
		t.Fatal(err)
	}
	// Place a valid envelope under the requested filename but retain the wrong key.
	if err := os.Rename(p.file("wrong-key"), p.file(key)); err != nil {
		t.Fatal(err)
	}
	got, err := p.Detail(context.Background(), CIRuns, Item{ID: "9"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Item.Title != "fresh" {
		t.Fatalf("got stale mismatched detail: %#v", got)
	}
}
