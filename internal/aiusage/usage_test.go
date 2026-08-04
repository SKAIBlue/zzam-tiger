package aiusage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadHidesProvidersWithoutCredentials(t *testing.T) {
	if got := Load(t.TempDir()); len(got) != 0 {
		t.Fatalf("providers without credentials = %#v", got)
	}
}

func TestLoadClaudeStatsWithCredential(t *testing.T) {
	oldURL := claudeUsageURL
	claudeUsageURL = "http://127.0.0.1:1"
	defer func() { claudeUsageURL = oldURL }()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Local().Format("2006-01-02")
	data := fmt.Sprintf(`{"lastComputedDate":%q,"modelUsage":{"opus":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":20,"cacheCreationInputTokens":2}},"dailyModelTokens":[{"date":%q,"tokensByModel":{"opus":37}}]}`, today, today)
	if err := os.WriteFile(filepath.Join(dir, "stats-cache.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got := Load(home)
	if len(got) != 1 || got[0].Name != "Claude" || got[0].TotalTokens != 37 || got[0].MonthlyTokens != 37 || len(got[0].Days) != 1 {
		t.Fatalf("Claude usage = %#v", got)
	}
	if len(got[0].Models) != 1 || got[0].Models[0] != (ModelUsage{Model: "opus", Input: 32, Cached: 22, CacheWrite: 2, Output: 5, MonthlyInput: 32, MonthlyCached: 22, MonthlyWrite: 2, MonthlyOutput: 5}) {
		t.Fatalf("Claude model usage = %#v", got[0].Models)
	}
}

func TestClaudeUsageAPIProvidesFiveHourAndWeeklyLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Error("missing OAuth usage headers")
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":12.5,"resets_at":"2026-07-28T15:00:00Z"},"seven_day":{"utilization":34,"resets_at":"2026-08-01T00:00:00Z"}}`))
	}))
	defer server.Close()
	old := claudeUsageURL
	claudeUsageURL = server.URL
	defer func() { claudeUsageURL = old }()
	path := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	limits, notice := fetchClaudeLimits(path)
	if notice != "" || len(limits) != 2 || limits[0].Used != 12.5 || limits[1].Used != 34 {
		t.Fatalf("limits = %#v, notice=%q", limits, notice)
	}
}

func TestCodexUsageAPIUsesLocalCredentialForLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing access token")
		}
		if r.Header.Get("ChatGPT-Account-ID") != "account-1" || r.Header.Get("OpenAI-Beta") != "codex-1" {
			t.Error("missing Codex account headers")
		}
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":21,"limit_window_seconds":18000,"reset_at":1785250800},"secondary_window":{"used_percent":42,"limit_window_seconds":604800,"reset_at":1785682800}}}`))
	}))
	defer server.Close()
	old := codexUsageURL
	codexUsageURL = server.URL
	defer func() { codexUsageURL = old }()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"secret","account_id":"account-1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	limits, notice := fetchCodexLimits(path)
	if notice != "" || len(limits) != 2 || limits[0].Used != 21 || limits[0].Label != "5 hours" || limits[1].Used != 42 || limits[1].Label != "1 week" {
		t.Fatalf("limits = %#v, notice=%q", limits, notice)
	}
}

func TestCodexLimitsUseSharedTTLCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":21,"limit_window_seconds":18000,"reset_at":1785250800}}}`))
	}))
	defer server.Close()
	old := codexUsageURL
	codexUsageURL = server.URL
	defer func() { codexUsageURL = old }()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		limits, notice := fetchCodexLimits(path)
		if notice != "" || len(limits) != 1 || limits[0].Used != 21 {
			t.Fatalf("limits = %#v, notice=%q", limits, notice)
		}
	}
	if requests != 1 {
		t.Fatalf("API requests = %d, want one request within cache TTL", requests)
	}
}

func TestCodexLimitsCoalesceConcurrentRefreshes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":21,"limit_window_seconds":18000,"reset_at":1785250800}}}`))
	}))
	defer server.Close()
	old := codexUsageURL
	codexUsageURL = server.URL
	defer func() { codexUsageURL = old }()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			limits, notice := fetchCodexLimits(path)
			if notice != "" || len(limits) != 1 {
				t.Errorf("limits = %#v, notice=%q", limits, notice)
			}
		}()
	}
	wait.Wait()
	if got := requests.Load(); got != 1 {
		t.Fatalf("concurrent API requests = %d, want one", got)
	}
}

func TestCodexActivityOnlyRescansChangedSessions(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	sessionsDir := filepath.Join(codexDir, "sessions", "2026", "07")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionsDir, "session.jsonl")
	event := func(tokens int) []byte {
		return []byte(`{"timestamp":"2026-07-28T12:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":` + strconv.Itoa(tokens) + `}}}}` + "\n")
	}
	if err := os.WriteFile(path, event(10), 0600); err != nil {
		t.Fatal(err)
	}
	first := LoadActivity(home)
	if len(first) != 1 || first[0].TotalTokens != 10 {
		t.Fatalf("initial activity = %#v", first)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	originalTime := info.ModTime()
	if err := os.WriteFile(path, event(20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, originalTime, originalTime); err != nil {
		t.Fatal(err)
	}
	cached := LoadActivity(home)
	if len(cached) != 1 || cached[0].TotalTokens != 10 {
		t.Fatalf("unchanged session was rescanned: %#v", cached)
	}
	changedTime := originalTime.Add(time.Second)
	if err := os.Chtimes(path, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	updated := LoadActivity(home)
	if len(updated) != 1 || updated[0].TotalTokens != 20 {
		t.Fatalf("changed session was not rescanned: %#v", updated)
	}
}

func TestCodexActivityAggregatesInputOutputAndModelChanges(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	sessionsDir := filepath.Join(codexDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().Local().Format(time.RFC3339)
	data := fmt.Sprintf(""+
		`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5"}}`+"\n"+
		`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":20,"total_tokens":120}}}}`+"\n"+
		`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5-mini"}}`+"\n"+
		`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":80,"output_tokens":30,"total_tokens":180}}}}`+"\n", timestamp, timestamp, timestamp, timestamp)
	if err := os.WriteFile(filepath.Join(sessionsDir, "session.jsonl"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got := LoadActivity(home)
	if len(got) != 1 || got[0].TotalTokens != 180 || got[0].MonthlyTokens != 180 {
		t.Fatalf("Codex activity = %#v", got)
	}
	want := []ModelUsage{
		{Model: "gpt-5", Input: 100, Cached: 60, Output: 20, MonthlyInput: 100, MonthlyCached: 60, MonthlyOutput: 20},
		{Model: "gpt-5-mini", Input: 50, Cached: 20, Output: 10, MonthlyInput: 50, MonthlyCached: 20, MonthlyOutput: 10},
	}
	if len(got[0].Models) != len(want) {
		t.Fatalf("Codex model usage = %#v", got[0].Models)
	}
	for i := range want {
		if got[0].Models[i] != want[i] {
			t.Fatalf("Codex model usage[%d] = %#v, want %#v", i, got[0].Models[i], want[i])
		}
	}
}

func TestGeminiActivityReadsCurrentChatRecords(t *testing.T) {
	home := t.TempDir()
	chatDir := filepath.Join(home, ".gemini", "tmp", "project", "chats")
	if err := os.MkdirAll(chatDir, 0700); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().Local().Format(time.RFC3339)
	data := fmt.Sprintf(""+
		`{"id":"response-1","timestamp":%q,"type":"gemini","model":"gemini-2.5-flash","tokens":{"input":100,"output":10,"cached":40,"thoughts":5,"total":115}}`+"\n"+
		`{"id":"response-1","timestamp":%q,"type":"gemini","model":"gemini-2.5-flash","tokens":{"input":100,"output":20,"cached":40,"thoughts":5,"total":125}}`+"\n", timestamp, timestamp)
	if err := os.WriteFile(filepath.Join(chatDir, "session.jsonl"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got := LoadActivity(home)
	if len(got) != 1 || got[0].Name != GeminiName || got[0].TotalTokens != 125 || got[0].MonthlyTokens != 125 || len(got[0].Models) != 1 {
		t.Fatalf("Gemini activity = %#v", got)
	}
	want := ModelUsage{Model: "gemini-2.5-flash", Input: 100, Cached: 40, Output: 25, MonthlyInput: 100, MonthlyCached: 40, MonthlyOutput: 25}
	if got[0].Models[0] != want {
		t.Fatalf("Gemini model usage = %#v, want %#v", got[0].Models[0], want)
	}
}

func TestEstimatedCostUsesUncachedCachedAndOutputPrices(t *testing.T) {
	usage := ModelUsage{Model: "gpt-5.5", Input: 1_000_000, Cached: 600_000, Output: 200_000}
	got, ok := usage.EstimatedCost(false)
	if !ok || got != 8.3 {
		t.Fatalf("estimated cost = %v, %v; want 8.3, true", got, ok)
	}
	if _, ok := (ModelUsage{Model: "unknown"}).EstimatedCost(false); ok {
		t.Fatal("unknown model unexpectedly has a price")
	}
}

func TestPricingCoversClaudeGeminiAndCodex(t *testing.T) {
	for _, model := range []string{"claude-opus-4-8", "gemini-3.5-flash", "gpt-5.6-sol"} {
		if _, ok := (ModelUsage{Model: model}).EstimatedCost(false); !ok {
			t.Fatalf("model %q has no price", model)
		}
	}
}
