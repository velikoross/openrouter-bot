// Package api contains small HTTP helpers for OpenRouter-compatible APIs.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// IsFree reports whether both prompt and completion are free. Models without
// pricing info (local LLM servers) are not considered free.
func (m Model) IsFree() bool {
	return isZero(m.Pricing.Prompt) && (m.Pricing.Completion == "" || isZero(m.Pricing.Completion))
}

func isZero(s string) bool {
	if s == "" {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && f == 0
}

// PricePerMillion formats "$prompt/$completion per 1M tokens".
func (m Model) PricePerMillion() string {
	p, err1 := strconv.ParseFloat(m.Pricing.Prompt, 64)
	c, err2 := strconv.ParseFloat(m.Pricing.Completion, 64)
	if err1 != nil || err2 != nil {
		return ""
	}
	if p == 0 && c == 0 {
		return "free"
	}
	return fmt.Sprintf("$%s/$%s", trimFloat(p*1e6), trimFloat(c*1e6))
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// RetryDelays for the generation-cost lookup (the generation is usually
	// not available immediately after the completion finishes).
	RetryDelays []time.Duration
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		// ~2 minutes in total: on OpenRouter the generation stats sometimes
		// appear only after tens of seconds.
		RetryDelays: []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second},
	}
}

func (c *Client) get(ctx context.Context, path string, out interface{}) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return 0, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return resp.StatusCode, fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, snippet)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("GET %s: parse json: %w", path, err)
	}
	return resp.StatusCode, nil
}

// ListModels returns all models sorted by id.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var resp struct {
		Data []Model `json:"data"`
	}
	if _, err := c.get(ctx, "/models", &resp); err != nil {
		return nil, err
	}
	sort.Slice(resp.Data, func(i, j int) bool { return resp.Data[i].ID < resp.Data[j].ID })
	return resp.Data, nil
}

// GenerationCost returns the cost of a finished generation in USD. OpenRouter
// returns 404 for a few seconds after the completion, so it retries.
func (c *Client) GenerationCost(ctx context.Context, id string) (float64, error) {
	var resp struct {
		Data struct {
			TotalCost float64 `json:"total_cost"`
		} `json:"data"`
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		status, err := c.get(ctx, "/generation?id="+id, &resp)
		if err == nil {
			return resp.Data.TotalCost, nil
		}
		lastErr = err
		retryable := status == 0 || status == http.StatusNotFound || status == http.StatusTooManyRequests || status >= 500
		if !retryable || attempt >= len(c.RetryDelays) {
			return 0, lastErr
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(c.RetryDelays[attempt]):
		}
	}
}
