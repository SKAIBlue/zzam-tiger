package aiusage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type geminiMessage struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Model     string    `json:"model"`
	Tokens    *struct {
		Input    int64 `json:"input"`
		Output   int64 `json:"output"`
		Cached   int64 `json:"cached"`
		Thoughts int64 `json:"thoughts"`
		Total    int64 `json:"total"`
	} `json:"tokens"`
}

func loadGeminiActivity(home string) (Provider, bool) {
	root := filepath.Join(home, ".gemini", "tmp")
	models := make(map[string]ModelUsage)
	daily := make(map[string]int64)
	var updated time.Time
	var totalTokens int64
	var monthlyTokens int64
	found := false
	currentMonth := time.Now().Local().Format("2006-01")
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || (filepath.Ext(path) != ".jsonl" && filepath.Ext(path) != ".json") || !strings.Contains(path, string(filepath.Separator)+"chats"+string(filepath.Separator)) {
			return nil
		}
		messages, ok := readGeminiMessages(path)
		if !ok {
			return nil
		}
		for _, message := range messages {
			if message.Type != "gemini" || message.Model == "" || message.Tokens == nil {
				continue
			}
			found = true
			usage := models[message.Model]
			usage.Model = message.Model
			usage.Input += message.Tokens.Input
			usage.Cached += message.Tokens.Cached
			usage.Output += message.Tokens.Output + message.Tokens.Thoughts
			if message.Timestamp.Local().Format("2006-01") == currentMonth {
				usage.MonthlyInput += message.Tokens.Input
				usage.MonthlyCached += message.Tokens.Cached
				usage.MonthlyOutput += message.Tokens.Output + message.Tokens.Thoughts
				monthlyTokens += message.Tokens.Total
			}
			models[message.Model] = usage
			totalTokens += message.Tokens.Total
			if !message.Timestamp.IsZero() {
				daily[message.Timestamp.Local().Format("2006-01-02")] += message.Tokens.Total
				if message.Timestamp.After(updated) {
					updated = message.Timestamp
				}
			}
		}
		return nil
	})
	if !found {
		return Provider{}, false
	}
	provider := Provider{Name: GeminiName, ActivityLoaded: true, LimitsLoaded: true, Notice: "Subscription limits unavailable", Updated: updated}
	for _, usage := range models {
		provider.Models = append(provider.Models, usage)
	}
	provider.TotalTokens = totalTokens
	provider.MonthlyTokens = monthlyTokens
	for date, tokens := range daily {
		provider.Days = append(provider.Days, Day{Date: date, Tokens: tokens})
	}
	sortModelUsage(provider.Models)
	sort.Slice(provider.Days, func(i, j int) bool { return provider.Days[i].Date < provider.Days[j].Date })
	return provider, true
}

func readGeminiMessages(path string) ([]geminiMessage, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	if filepath.Ext(path) == ".json" {
		var conversation struct {
			Messages []geminiMessage `json:"messages"`
		}
		if json.NewDecoder(file).Decode(&conversation) != nil {
			return nil, false
		}
		return conversation.Messages, true
	}
	byID := make(map[string]geminiMessage)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var message geminiMessage
		if json.Unmarshal(scanner.Bytes(), &message) == nil && message.ID != "" {
			byID[message.ID] = message
		}
	}
	messages := make([]geminiMessage, 0, len(byID))
	for _, message := range byID {
		messages = append(messages, message)
	}
	return messages, scanner.Err() == nil
}
