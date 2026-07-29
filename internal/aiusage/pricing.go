package aiusage

import "strings"

// Prices are standard paid API prices in USD per one million text tokens.
// They are estimates only: subscription plans, tools, long-context premiums,
// regional processing, batch discounts, and cache storage are not included.
// Sources: https://developers.openai.com/api/docs/models,
// https://platform.claude.com/docs/en/about-claude/pricing, and
// https://ai.google.dev/gemini-api/docs/pricing.
const pricingEffectiveDate = "2026-07-29"

type modelPrice struct {
	Input, Cached, CacheWrite, Output float64
}

func PriceEffectiveDate() string { return pricingEffectiveDate }

func priceForModel(model string) (modelPrice, bool) {
	name := strings.ToLower(model)
	prices := []struct {
		prefix string
		price  modelPrice
	}{
		{"gpt-5.6-sol", modelPrice{5, .5, 5, 30}},
		{"gpt-5.6-terra", modelPrice{2.5, .25, 2.5, 15}},
		{"gpt-5.6-luna", modelPrice{1, .1, 1, 6}},
		{"gpt-5.5", modelPrice{5, .5, 5, 30}},
		{"gpt-5.3-codex", modelPrice{1.75, .175, 1.75, 14}},
		{"gpt-5.2-codex", modelPrice{1.75, .175, 1.75, 14}},
		{"gpt-5-mini", modelPrice{.25, .025, .25, 2}},
		{"gpt-5-nano", modelPrice{.05, .005, .05, .4}},
		{"gpt-5", modelPrice{1.25, .125, 1.25, 10}},
		{"claude-opus-4-8", modelPrice{5, .5, 6.25, 25}},
		{"claude-opus-4-7", modelPrice{5, .5, 6.25, 25}},
		{"claude-opus-4-6", modelPrice{5, .5, 6.25, 25}},
		{"claude-sonnet-4-6", modelPrice{3, .3, 3.75, 15}},
		{"claude-haiku-4-5", modelPrice{1, .1, 1.25, 5}},
		{"gemini-3.6-flash", modelPrice{1.5, .15, 1.5, 7.5}},
		{"gemini-3.5-flash-lite", modelPrice{.3, .03, .3, 2.5}},
		{"gemini-3.5-flash", modelPrice{1.5, .15, 1.5, 9}},
		{"gemini-3.1-pro", modelPrice{2, .2, 2, 12}},
		{"gemini-3.1-flash-lite", modelPrice{.25, .025, .25, 1.5}},
		{"gemini-3-flash", modelPrice{.5, .05, .5, 3}},
		{"gemini-2.5-pro", modelPrice{1.25, .125, 1.25, 10}},
		{"gemini-2.5-flash-lite", modelPrice{.1, .01, .1, .4}},
		{"gemini-2.5-flash", modelPrice{.3, .03, .3, 2.5}},
	}
	for _, item := range prices {
		if strings.HasPrefix(name, item.prefix) {
			return item.price, true
		}
	}
	return modelPrice{}, false
}

func (usage ModelUsage) EstimatedCost(monthly bool) (float64, bool) {
	price, ok := priceForModel(usage.Model)
	if !ok {
		return 0, false
	}
	input, cached, write, output := usage.Input, usage.Cached, usage.CacheWrite, usage.Output
	if monthly {
		input, cached, write, output = usage.MonthlyInput, usage.MonthlyCached, usage.MonthlyWrite, usage.MonthlyOutput
	}
	uncached := max(int64(0), input-cached)
	read := max(int64(0), cached-write)
	cost := float64(uncached)*price.Input + float64(read)*price.Cached + float64(write)*price.CacheWrite + float64(output)*price.Output
	return cost / 1_000_000, true
}

func EstimatedCosts(models []ModelUsage) (total, monthly float64, complete bool) {
	complete = true
	for _, model := range models {
		modelTotal, ok := model.EstimatedCost(false)
		if !ok {
			complete = false
			continue
		}
		modelMonthly, _ := model.EstimatedCost(true)
		total += modelTotal
		monthly += modelMonthly
	}
	return total, monthly, complete
}
