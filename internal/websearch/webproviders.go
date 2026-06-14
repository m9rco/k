package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// webProvider performs one text-search backend. It must never panic and should
// return ([], err) on failure so the chain can fall back to the next provider.
type webProvider func(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error)

// webChain returns the ordered provider chain. Key-based providers are placed
// first when their env keys are present (higher quality), with zero-key
// ddg_html as the reliable scraping fallback. (bing_html dropped: Bing serves a
// CAPTCHA page to datacenter IPs, returning zero organic results.)
func webChain() []webProvider {
	var chain []webProvider
	if os.Getenv("TAVILY_API_KEY") != "" {
		chain = append(chain, searchTavily)
	}
	if os.Getenv("SERPER_API_KEY") != "" {
		chain = append(chain, searchSerper)
	}
	if os.Getenv("BOCHA_API_KEY") != "" {
		chain = append(chain, searchBocha)
	}
	chain = append(chain, searchDDGHTML, searchDDGInstant)
	return chain
}

// runWebChain tries each provider in order, returning the first non-empty,
// domain-filtered, deduplicated result set.
func runWebChain(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error) {
	var lastErr error
	for _, p := range webChain() {
		res, err := p(ctx, c, query, limit)
		if err != nil {
			lastErr = err
			continue
		}
		res = filterWebResults(res, limit)
		if len(res) > 0 {
			return res, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// ── domain filtering + dedup ─────────────────────────────────────────────────

var webBlockedDomains = []string{
	"chatgpt-chinese.com", "openai-chinese.com", "calendar-365.com", "theyear2026.com",
}

func filterWebResults(items []WebResult, limit int) []WebResult {
	seen := map[string]bool{}
	out := make([]WebResult, 0, len(items))
	for _, r := range items {
		if r.URL == "" || seen[r.URL] {
			continue
		}
		host := domainOf(r.URL)
		blocked := false
		for _, b := range webBlockedDomains {
			if strings.Contains(host, b) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// ── ddg_html (zero-key organic-results scraper) ──────────────────────────────

const bingUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var (
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
	// DDG html result anchor: <a ... class="result__a" href="...">title</a>
	ddgAnchorRe = regexp.MustCompile(`(?s)class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	// snippet: <a ... class="result__snippet" ...>snippet</a>
	ddgSnippetRe = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
)

func stripHTML(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagStripRe.ReplaceAllString(s, "")))
}

// decodeDDGRedirect unwraps DDG's //duckduckgo.com/l/?uddg=<encoded real url>.
func decodeDDGRedirect(href string) string {
	href = html.UnescapeString(href)
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	if !strings.Contains(href, "duckduckgo.com/l/") {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if real := u.Query().Get("uddg"); real != "" {
		return real
	}
	return href
}

func searchDDGHTML(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error) {
	u := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", bingUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ddg_html: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	anchors := ddgAnchorRe.FindAllSubmatch(body, -1)
	snippets := ddgSnippetRe.FindAllSubmatch(body, -1)
	var out []WebResult
	for i, a := range anchors {
		href := decodeDDGRedirect(string(a[1]))
		if !strings.HasPrefix(href, "http") {
			continue
		}
		// skip DDG's own ad redirects (y.js / ad_domain)
		if strings.Contains(href, "duckduckgo.com/y.js") || strings.Contains(href, "ad_provider") {
			continue
		}
		title := stripHTML(string(a[2]))
		snippet := ""
		if i < len(snippets) {
			snippet = stripHTML(string(snippets[i][1]))
		}
		out = append(out, WebResult{Title: title, URL: href, Snippet: snippet})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ── DuckDuckGo Instant Answer (zero-key fallback) ────────────────────────────

func searchDDGInstant(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error) {
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; GameAssetBot/1.0)")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ddg instant: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var ddg struct {
		Abstract      string `json:"Abstract"`
		AbstractURL   string `json:"AbstractURL"`
		AbstractTitle string `json:"AbstractTitle"`
		RelatedTopics []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
		} `json:"RelatedTopics"`
	}
	if err := json.Unmarshal(raw, &ddg); err != nil {
		return nil, fmt.Errorf("ddg decode: %w", err)
	}
	var out []WebResult
	if ddg.Abstract != "" && ddg.AbstractURL != "" {
		out = append(out, WebResult{Title: ddg.AbstractTitle, URL: ddg.AbstractURL, Snippet: ddg.Abstract})
	}
	for _, t := range ddg.RelatedTopics {
		if len(out) >= limit {
			break
		}
		if t.FirstURL == "" {
			continue
		}
		out = append(out, WebResult{URL: t.FirstURL, Snippet: t.Text})
	}
	return out, nil
}

// ── Tavily (TAVILY_API_KEY) ──────────────────────────────────────────────────

func searchTavily(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error) {
	payload := map[string]any{
		"api_key": os.Getenv("TAVILY_API_KEY"), "query": query,
		"max_results": limit, "search_depth": "basic",
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()
	var data struct {
		Results []struct {
			Title, URL, Content string
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var out []WebResult
	for _, r := range data.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, WebResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

// ── Serper / Google proxy (SERPER_API_KEY) ───────────────────────────────────

func searchSerper(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error) {
	b, _ := json.Marshal(map[string]any{"q": query, "num": limit, "gl": "cn", "hl": "zh-cn"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", os.Getenv("SERPER_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper: %w", err)
	}
	defer resp.Body.Close()
	var data struct {
		Organic []struct {
			Title, Link, Snippet string
		} `json:"organic"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var out []WebResult
	for _, r := range data.Organic {
		if r.Link == "" {
			continue
		}
		out = append(out, WebResult{Title: r.Title, URL: r.Link, Snippet: r.Snippet})
	}
	return out, nil
}

// ── Bocha (BOCHA_API_KEY, 国产中文友好) ───────────────────────────────────────

func searchBocha(ctx context.Context, c *http.Client, query string, limit int) ([]WebResult, error) {
	b, _ := json.Marshal(map[string]any{"query": query, "summary": true, "count": limit, "freshness": "noLimit"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.bochaai.com/v1/web-search", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("BOCHA_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bocha: %w", err)
	}
	defer resp.Body.Close()
	var data struct {
		Data struct {
			WebPages struct {
				Value []struct {
					Name, URL, Snippet, Summary string
				} `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var out []WebResult
	for _, r := range data.Data.WebPages.Value {
		if r.URL == "" {
			continue
		}
		snippet := r.Snippet
		if snippet == "" {
			snippet = r.Summary
		}
		out = append(out, WebResult{Title: r.Name, URL: r.URL, Snippet: snippet})
	}
	return out, nil
}
