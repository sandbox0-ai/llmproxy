package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content,omitempty"`
}

type Client struct {
	APIKey string
	HTTP   *http.Client
}

func (c Client) Search(ctx context.Context, query string) (string, []Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil, fmt.Errorf("query is required")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return "", nil, fmt.Errorf("web search is not configured")
	}
	if strings.HasPrefix(c.APIKey, "BSA") {
		return c.brave(ctx, query)
	}
	return c.tavily(ctx, query)
}

func (c Client) tavily(ctx context.Context, query string) (string, []Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]any{
		"api_key":        c.APIKey,
		"query":          query,
		"search_depth":   "basic",
		"include_answer": true,
		"max_results":    5,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("tavily returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil, err
	}
	results := make([]Result, 0, len(payload.Results))
	for _, item := range payload.Results {
		results = append(results, Result{Title: item.Title, URL: item.URL, Content: item.Content})
	}
	return format(query, payload.Answer, results), results, nil
}

func (c Client) brave(ctx context.Context, query string) (string, []Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	u := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=5"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", c.APIKey)
	resp, err := c.http().Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("brave search returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil, err
	}
	results := make([]Result, 0, len(payload.Web.Results))
	for _, item := range payload.Web.Results {
		results = append(results, Result{Title: item.Title, URL: item.URL, Content: item.Description})
	}
	return format(query, "", results), results, nil
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func format(query, answer string, results []Result) string {
	var b strings.Builder
	b.WriteString("Web search results for: ")
	b.WriteString(query)
	if strings.TrimSpace(answer) != "" {
		b.WriteString("\n\nAnswer: ")
		b.WriteString(answer)
	}
	for i, r := range results {
		b.WriteString(fmt.Sprintf("\n\n%d. %s\nURL: %s", i+1, r.Title, r.URL))
		if strings.TrimSpace(r.Content) != "" {
			b.WriteString("\n")
			b.WriteString(r.Content)
		}
	}
	return b.String()
}
