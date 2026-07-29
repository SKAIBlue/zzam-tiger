package aiusage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const (
	ClaudeName = "Claude"
	CodexName  = "Codex"
	GeminiName = "Gemini"

	limitCacheTTL = 5 * time.Minute
	lockWait      = 12 * time.Second
	staleLockAge  = 2 * time.Minute
)

type Limit struct {
	Label string
	Used  float64
	Reset time.Time
}

type Day struct {
	Date   string
	Tokens int64
}

type ModelUsage struct {
	Model         string
	Input         int64
	Cached        int64
	CacheWrite    int64
	Output        int64
	MonthlyInput  int64
	MonthlyCached int64
	MonthlyWrite  int64
	MonthlyOutput int64
}

type Provider struct {
	Name           string
	TotalTokens    int64
	MonthlyTokens  int64
	Models         []ModelUsage
	Limits         []Limit
	Days           []Day
	Updated        time.Time
	Notice         string
	LimitsLoaded   bool
	ActivityLoaded bool
}

var claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
var codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// Load retains the package's original all-at-once interface. Interactive users
// should call LoadLimits and LoadActivity independently so one cannot block the
// other from being displayed.
func Load(home string) []Provider {
	return mergeProviders(LoadLimits(home), LoadActivity(home))
}

// LoadLimits retrieves subscription limit windows. Provider requests run in
// parallel and share a disk cache across zt processes.
func LoadLimits(home string) []Provider {
	type result struct {
		provider Provider
		ok       bool
	}
	results := make(chan result, 2)
	go func() {
		provider, ok := loadClaudeLimits(home)
		results <- result{provider, ok}
	}()
	go func() {
		provider, ok := loadCodexLimits(home)
		results <- result{provider, ok}
	}()

	providers := make([]Provider, 0, 2)
	for range 2 {
		item := <-results
		if item.ok {
			providers = append(providers, item.provider)
		}
	}
	sortProviders(providers)
	return providers
}

// LoadActivity reads local token history. Codex session summaries are cached by
// file size and modification time, so unchanged JSONL files are not rescanned.
func LoadActivity(home string) []Provider {
	providers := make([]Provider, 0, 2)
	if provider, ok := loadClaudeActivity(home); ok {
		providers = append(providers, provider)
	}
	if provider, ok := loadCodexActivity(home); ok {
		providers = append(providers, provider)
	}
	if provider, ok := loadGeminiActivity(home); ok {
		providers = append(providers, provider)
	}
	sortProviders(providers)
	return providers
}

func mergeProviders(groups ...[]Provider) []Provider {
	byName := make(map[string]Provider)
	for _, group := range groups {
		for _, update := range group {
			provider := byName[update.Name]
			provider.Name = update.Name
			if update.LimitsLoaded {
				provider.Limits = update.Limits
				provider.Notice = update.Notice
				provider.LimitsLoaded = true
			}
			if update.ActivityLoaded {
				provider.TotalTokens = update.TotalTokens
				provider.MonthlyTokens = update.MonthlyTokens
				provider.Models = update.Models
				provider.Days = update.Days
				provider.Updated = update.Updated
				provider.ActivityLoaded = true
			}
			byName[update.Name] = provider
		}
	}
	providers := make([]Provider, 0, len(byName))
	for _, provider := range byName {
		providers = append(providers, provider)
	}
	sortProviders(providers)
	return providers
}

func sortProviders(providers []Provider) {
	sort.Slice(providers, func(i, j int) bool {
		order := func(name string) int {
			if name == ClaudeName {
				return 0
			}
			if name == CodexName {
				return 1
			}
			return 2
		}
		return order(providers[i].Name) < order(providers[j].Name)
	})
}

func claudeAvailable(home string) bool {
	credential := filepath.Join(home, ".claude", ".credentials.json")
	if _, err := os.Stat(credential); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(home, ".claude", "stats-cache.json"))
	return err == nil
}

func loadClaudeActivity(home string) (Provider, bool) {
	if !claudeAvailable(home) {
		return Provider{}, false
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "stats-cache.json"))
	if err != nil {
		return Provider{}, false
	}
	var data struct {
		Last   string `json:"lastComputedDate"`
		Models map[string]struct {
			Input       int64 `json:"inputTokens"`
			Output      int64 `json:"outputTokens"`
			CacheRead   int64 `json:"cacheReadInputTokens"`
			CacheCreate int64 `json:"cacheCreationInputTokens"`
		} `json:"modelUsage"`
		Daily []struct {
			Date   string           `json:"date"`
			Tokens map[string]int64 `json:"tokensByModel"`
		} `json:"dailyModelTokens"`
	}
	if json.Unmarshal(b, &data) != nil {
		return Provider{}, false
	}
	provider := Provider{Name: ClaudeName, ActivityLoaded: true}
	monthStart := time.Now().Local().Format("2006-01")
	monthlyTokens := make(map[string]int64)
	for _, day := range data.Daily {
		if len(day.Date) >= 7 && day.Date[:7] == monthStart {
			for model, tokens := range day.Tokens {
				monthlyTokens[model] += tokens
				provider.MonthlyTokens += tokens
			}
		}
	}
	for model, value := range data.Models {
		provider.TotalTokens += value.Input + value.Output + value.CacheRead + value.CacheCreate
		cached := value.CacheRead + value.CacheCreate
		usage := ModelUsage{Model: model, Input: value.Input + cached, Cached: cached, CacheWrite: value.CacheCreate, Output: value.Output}
		total := usage.Input + usage.Output
		if total > 0 && monthlyTokens[model] > 0 {
			ratio := float64(monthlyTokens[model]) / float64(total)
			usage.MonthlyInput = int64(float64(usage.Input) * ratio)
			usage.MonthlyCached = int64(float64(usage.Cached) * ratio)
			usage.MonthlyWrite = int64(float64(usage.CacheWrite) * ratio)
			usage.MonthlyOutput = int64(float64(usage.Output) * ratio)
		}
		provider.Models = append(provider.Models, usage)
	}
	sortModelUsage(provider.Models)
	for _, day := range data.Daily {
		var tokens int64
		for _, value := range day.Tokens {
			tokens += value
		}
		provider.Days = append(provider.Days, Day{day.Date, tokens})
	}
	provider.Updated, _ = time.Parse("2006-01-02", data.Last)
	return provider, true
}

func loadClaudeLimits(home string) (Provider, bool) {
	if !claudeAvailable(home) {
		return Provider{}, false
	}
	limits, notice := fetchClaudeLimits(filepath.Join(home, ".claude", ".credentials.json"))
	return Provider{Name: ClaudeName, Limits: limits, Notice: notice, LimitsLoaded: true}, true
}

type limitsCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	RetryAt   time.Time `json:"retry_at"`
	Limits    []Limit   `json:"limits"`
	Notice    string    `json:"notice,omitempty"`
}

type limitFetch struct {
	limits  []Limit
	notice  string
	retryAt time.Time
	cache   bool
}

func fetchClaudeLimits(credentialPath string) ([]Limit, string) {
	cachePath := filepath.Join(filepath.Dir(credentialPath), ".zt-usage-limits-cache.json")
	return loadCachedLimits(cachePath, func() limitFetch {
		var raw []byte
		if data, err := os.ReadFile(credentialPath); err == nil {
			raw = data
		} else {
			data, err := exec.Command("security", "find-generic-password", "-w", "-s", "Claude Code-credentials").Output()
			if err != nil {
				return limitFetch{notice: "Credential unavailable"}
			}
			raw = data
		}
		var credential struct {
			OAuth struct {
				AccessToken string `json:"accessToken"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(raw, &credential) != nil || credential.OAuth.AccessToken == "" {
			return limitFetch{notice: "Credential unavailable"}
		}
		req, err := http.NewRequest(http.MethodGet, claudeUsageURL, nil)
		if err != nil {
			return limitFetch{notice: "Usage API request failed"}
		}
		req.Header.Set("Authorization", "Bearer "+credential.OAuth.AccessToken)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		req.Header.Set("User-Agent", "claude-code/2.1")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return limitFetch{notice: "Usage API request failed"}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			retryAt := time.Now().Add(time.Duration(max(60, seconds)) * time.Second)
			return limitFetch{notice: "Usage API rate limited · retry at " + retryAt.Local().Format("15:04"), retryAt: retryAt, cache: true}
		}
		if resp.StatusCode != http.StatusOK {
			return limitFetch{notice: "Usage API returned " + resp.Status}
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return limitFetch{notice: "Usage API response failed"}
		}
		var usage struct {
			Five *struct {
				Utilization float64 `json:"utilization"`
				Reset       string  `json:"resets_at"`
			} `json:"five_hour"`
			Week *struct {
				Utilization float64 `json:"utilization"`
				Reset       string  `json:"resets_at"`
			} `json:"seven_day"`
		}
		if json.Unmarshal(body, &usage) != nil {
			return limitFetch{notice: "Usage API response failed"}
		}
		limits := make([]Limit, 0, 2)
		for _, item := range []struct {
			label string
			value *struct {
				Utilization float64 `json:"utilization"`
				Reset       string  `json:"resets_at"`
			}
		}{{"5 hours", usage.Five}, {"1 week", usage.Week}} {
			if item.value == nil {
				continue
			}
			reset, _ := time.Parse(time.RFC3339Nano, item.value.Reset)
			limits = append(limits, Limit{item.label, item.value.Utilization, reset})
		}
		notice := ""
		if len(limits) == 0 {
			notice = "Limit data is not available for this account"
		}
		return limitFetch{limits: limits, notice: notice, cache: true}
	})
}

func loadCodexActivity(home string) (Provider, bool) {
	authPath := filepath.Join(home, ".codex", "auth.json")
	if _, err := os.Stat(authPath); err != nil {
		return Provider{}, false
	}
	root := filepath.Join(home, ".codex", "sessions")
	cachePath := filepath.Join(home, ".codex", ".zt-usage-activity-cache.json")
	unlock, acquired := acquireCacheLock(cachePath + ".lock")
	if !acquired {
		if cache, ok := readCodexActivityCache(cachePath); ok {
			return providerFromCodexCache(cache), true
		}
		return Provider{Name: CodexName, ActivityLoaded: true}, true
	}
	defer unlock()

	cache, _ := readCodexActivityCache(cachePath)
	month := time.Now().Local().Format("2006-01")
	if cache.Version != 4 || cache.Month != month || cache.Files == nil {
		cache = codexActivityCache{Version: 4, Month: month, Files: make(map[string]codexSessionCache)}
	}
	next := codexActivityCache{Version: 4, Month: month, Files: make(map[string]codexSessionCache)}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		key, err := filepath.Rel(root, path)
		if err != nil {
			key = path
		}
		cached, found := cache.Files[key]
		if found && cached.Size == info.Size() && cached.ModTime == info.ModTime().UnixNano() {
			next.Files[key] = cached
			return nil
		}
		summary, ok := scanCodexSession(path)
		if !ok {
			return nil
		}
		summary.Size = info.Size()
		summary.ModTime = info.ModTime().UnixNano()
		if summary.Day == "" && summary.Tokens > 0 {
			summary.Day = info.ModTime().Format("2006-01-02")
		}
		next.Files[key] = summary
		return nil
	})
	_ = writeJSONAtomically(cachePath, next)
	return providerFromCodexCache(next), true
}

type codexActivityCache struct {
	Version int                          `json:"version"`
	Month   string                       `json:"month,omitempty"`
	Files   map[string]codexSessionCache `json:"files"`
}

type codexSessionCache struct {
	Size          int64        `json:"size"`
	ModTime       int64        `json:"mod_time"`
	Tokens        int64        `json:"tokens"`
	MonthlyTokens int64        `json:"monthly_tokens,omitempty"`
	Models        []ModelUsage `json:"models,omitempty"`
	Day           string       `json:"day,omitempty"`
	Updated       time.Time    `json:"updated,omitempty"`
}

func readCodexActivityCache(path string) (codexActivityCache, bool) {
	var cache codexActivityCache
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &cache) != nil {
		return codexActivityCache{}, false
	}
	return cache, true
}

func scanCodexSession(path string) (codexSessionCache, bool) {
	file, err := os.Open(path)
	if err != nil {
		return codexSessionCache{}, false
	}
	defer file.Close()
	var summary codexSessionCache
	byModel := make(map[string]ModelUsage)
	model := "Unknown model"
	currentMonth := time.Now().Local().Format("2006-01")
	var previous struct {
		Input  int64
		Cached int64
		Output int64
		Total  int64
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Type  string `json:"type"`
				Model string `json:"model"`
				Info  *struct {
					Total struct {
						Input  int64 `json:"input_tokens"`
						Cached int64 `json:"cached_input_tokens"`
						Output int64 `json:"output_tokens"`
						Total  int64 `json:"total_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "turn_context" && event.Payload.Model != "" {
			model = event.Payload.Model
			continue
		}
		if event.Type != "event_msg" || event.Payload.Type != "token_count" {
			continue
		}
		if event.Payload.Info != nil {
			total := event.Payload.Info.Total
			usage := byModel[model]
			usage.Model = model
			usage.Input += counterDelta(total.Input, previous.Input)
			usage.Cached += counterDelta(total.Cached, previous.Cached)
			usage.Output += counterDelta(total.Output, previous.Output)
			if event.Timestamp.Local().Format("2006-01") == currentMonth {
				usage.MonthlyInput += counterDelta(total.Input, previous.Input)
				usage.MonthlyCached += counterDelta(total.Cached, previous.Cached)
				usage.MonthlyOutput += counterDelta(total.Output, previous.Output)
			}
			byModel[model] = usage
			summary.Tokens += counterDelta(total.Total, previous.Total)
			if event.Timestamp.Local().Format("2006-01") == currentMonth {
				summary.MonthlyTokens += counterDelta(total.Total, previous.Total)
			}
			previous.Input, previous.Cached = total.Input, total.Cached
			previous.Output, previous.Total = total.Output, total.Total
		}
		if event.Timestamp.After(summary.Updated) {
			summary.Updated = event.Timestamp
		}
	}
	for _, usage := range byModel {
		summary.Models = append(summary.Models, usage)
	}
	sortModelUsage(summary.Models)
	if summary.Tokens > 0 && !summary.Updated.IsZero() {
		summary.Day = summary.Updated.Local().Format("2006-01-02")
	}
	return summary, scanner.Err() == nil
}

func providerFromCodexCache(cache codexActivityCache) Provider {
	provider := Provider{Name: CodexName, ActivityLoaded: true}
	daily := make(map[string]int64)
	models := make(map[string]ModelUsage)
	for _, session := range cache.Files {
		provider.TotalTokens += session.Tokens
		provider.MonthlyTokens += session.MonthlyTokens
		if session.Tokens > 0 && session.Day != "" {
			daily[session.Day] += session.Tokens
		}
		if session.Updated.After(provider.Updated) {
			provider.Updated = session.Updated
		}
		for _, usage := range session.Models {
			total := models[usage.Model]
			total.Model = usage.Model
			total.Input += usage.Input
			total.Cached += usage.Cached
			total.Output += usage.Output
			total.MonthlyInput += usage.MonthlyInput
			total.MonthlyCached += usage.MonthlyCached
			total.MonthlyOutput += usage.MonthlyOutput
			models[usage.Model] = total
		}
	}
	for _, usage := range models {
		provider.Models = append(provider.Models, usage)
	}
	sortModelUsage(provider.Models)
	for date, tokens := range daily {
		provider.Days = append(provider.Days, Day{date, tokens})
	}
	sort.Slice(provider.Days, func(i, j int) bool { return provider.Days[i].Date < provider.Days[j].Date })
	return provider
}

func counterDelta(current, previous int64) int64 {
	if current >= previous {
		return current - previous
	}
	return current
}

func sortModelUsage(models []ModelUsage) {
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
}

func loadCodexLimits(home string) (Provider, bool) {
	authPath := filepath.Join(home, ".codex", "auth.json")
	if _, err := os.Stat(authPath); err != nil {
		return Provider{}, false
	}
	limits, notice := fetchCodexLimits(authPath)
	return Provider{Name: CodexName, Limits: limits, Notice: notice, LimitsLoaded: true}, true
}

func fetchCodexLimits(authPath string) ([]Limit, string) {
	cachePath := filepath.Join(filepath.Dir(authPath), ".zt-usage-limits-cache.json")
	return loadCachedLimits(cachePath, func() limitFetch {
		raw, err := os.ReadFile(authPath)
		if err != nil {
			return limitFetch{notice: "Credential unavailable"}
		}
		var auth struct {
			Tokens struct {
				AccessToken string `json:"access_token"`
				AccountID   string `json:"account_id"`
			} `json:"tokens"`
		}
		if json.Unmarshal(raw, &auth) != nil || auth.Tokens.AccessToken == "" {
			return limitFetch{notice: "Credential unavailable"}
		}
		req, err := http.NewRequest(http.MethodGet, codexUsageURL, nil)
		if err != nil {
			return limitFetch{notice: "Usage API request failed"}
		}
		req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
		req.Header.Set("OpenAI-Beta", "codex-1")
		if auth.Tokens.AccountID != "" {
			req.Header.Set("ChatGPT-Account-ID", auth.Tokens.AccountID)
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return limitFetch{notice: "Usage API request failed"}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return limitFetch{notice: "Usage API returned " + resp.Status}
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return limitFetch{notice: "Usage API response failed"}
		}
		var usage struct {
			RateLimit struct {
				PrimaryWindow *struct {
					Used          float64 `json:"used_percent"`
					WindowSeconds int     `json:"limit_window_seconds"`
					Reset         int64   `json:"reset_at"`
				} `json:"primary_window"`
				SecondaryWindow *struct {
					Used          float64 `json:"used_percent"`
					WindowSeconds int     `json:"limit_window_seconds"`
					Reset         int64   `json:"reset_at"`
				} `json:"secondary_window"`
				Primary *struct {
					Used    float64 `json:"used_percent"`
					Minutes int     `json:"window_minutes"`
					Reset   int64   `json:"resets_at"`
				} `json:"primary"`
				Secondary *struct {
					Used    float64 `json:"used_percent"`
					Minutes int     `json:"window_minutes"`
					Reset   int64   `json:"resets_at"`
				} `json:"secondary"`
			} `json:"rate_limit"`
		}
		if json.Unmarshal(body, &usage) != nil {
			return limitFetch{notice: "Usage API response failed"}
		}
		limits := make([]Limit, 0, 2)
		for _, window := range []*struct {
			Used          float64 `json:"used_percent"`
			WindowSeconds int     `json:"limit_window_seconds"`
			Reset         int64   `json:"reset_at"`
		}{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
			if window == nil {
				continue
			}
			label := "5 hours"
			if window.WindowSeconds >= 7*24*60*60 {
				label = "1 week"
			}
			limits = append(limits, Limit{label, window.Used, time.Unix(window.Reset, 0)})
		}
		if len(limits) == 0 {
			for _, window := range []*struct {
				Used    float64 `json:"used_percent"`
				Minutes int     `json:"window_minutes"`
				Reset   int64   `json:"resets_at"`
			}{usage.RateLimit.Primary, usage.RateLimit.Secondary} {
				if window == nil {
					continue
				}
				label := "5 hours"
				if window.Minutes >= 10080 {
					label = "1 week"
				}
				limits = append(limits, Limit{label, window.Used, time.Unix(window.Reset, 0)})
			}
		}
		notice := ""
		if len(limits) == 0 {
			notice = "Limit data is not available for this account"
		}
		return limitFetch{limits: limits, notice: notice, cache: true}
	})
}

func loadCachedLimits(cachePath string, fetch func() limitFetch) ([]Limit, string) {
	cache, _ := readLimitsCache(cachePath)
	if limitsCacheFresh(cache) {
		return cache.Limits, cache.Notice
	}
	if time.Now().Before(cache.RetryAt) {
		return cache.Limits, "Usage API retry at " + cache.RetryAt.Local().Format("15:04")
	}

	unlock, acquired := acquireCacheLock(cachePath + ".lock")
	if !acquired {
		cache, _ = readLimitsCache(cachePath)
		return cache.Limits, "Usage refresh is already in progress"
	}
	defer unlock()
	cache, _ = readLimitsCache(cachePath)
	if limitsCacheFresh(cache) {
		return cache.Limits, cache.Notice
	}
	if time.Now().Before(cache.RetryAt) {
		return cache.Limits, "Usage API retry at " + cache.RetryAt.Local().Format("15:04")
	}

	result := fetch()
	if result.cache {
		if len(result.limits) == 0 && len(cache.Limits) > 0 {
			result.limits = cache.Limits
		}
		cache = limitsCache{FetchedAt: time.Now(), RetryAt: result.retryAt, Limits: result.limits, Notice: result.notice}
		_ = writeJSONAtomically(cachePath, cache)
		return cache.Limits, cache.Notice
	}
	if len(cache.Limits) > 0 {
		return cache.Limits, result.notice
	}
	return nil, result.notice
}

func readLimitsCache(path string) (limitsCache, bool) {
	var cache limitsCache
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &cache) != nil {
		return limitsCache{}, false
	}
	return cache, true
}

func limitsCacheFresh(cache limitsCache) bool {
	return !cache.FetchedAt.IsZero() && time.Since(cache.FetchedAt) >= 0 && time.Since(cache.FetchedAt) < limitCacheTTL
}

func acquireCacheLock(path string) (func(), bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return func() {}, false
	}
	deadline := time.Now().Add(lockWait)
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(path) }, true
		}
		if !errors.Is(err, os.ErrExist) {
			return func() {}, false
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return func() {}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func writeJSONAtomically(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".zt-usage-cache-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
