// Package websearch provides dual-source image search (Sogou CN + Bing EN)
// and DuckDuckGo web text search — no API keys required.
package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// WebResult is one text search result.
type WebResult struct {
	Title   string
	URL     string
	Snippet string
}

// ImageResult is one image search result.
type ImageResult struct {
	URL    string
	Source string // domain of the source page
	Width  int
	Height int
}

// Source performs web and image searches without API keys.
// SearchImages accepts optional queryCN (for Sogou) and queryEN (for Bing);
// pass both for best results, or just queryCN for auto-fallback.
type Source interface {
	SearchWeb(ctx context.Context, query string, limit int) ([]WebResult, error)
	SearchImages(ctx context.Context, queryCN, queryEN string, limit int) ([]ImageResult, error)
}

// DefaultSource returns the Sogou+Bing dual-source implementation.
func DefaultSource() Source {
	return &sogouBingSource{client: &http.Client{Timeout: 15 * time.Second}}
}

type sogouBingSource struct{ client *http.Client }

// ── web search (DDG Instant Answer) ──────────────────────────────────────────

// SearchWeb runs the multi-provider fallback chain (key-based providers first
// when configured, then zero-key bing_html + DDG), returning the first
// non-empty, domain-filtered result set.
func (s *sogouBingSource) SearchWeb(ctx context.Context, query string, limit int) ([]WebResult, error) {
	if limit <= 0 {
		limit = 8
	}
	return runWebChain(ctx, s.client, query, limit)
}

// ── Sogou image scraper ───────────────────────────────────────────────────────

var browserHeaders = map[string]string{
	"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
	"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
}

func (s *sogouBingSource) fetchHTML(ctx context.Context, rawURL string, extraHeaders ...map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range browserHeaders {
		req.Header.Set(k, v)
	}
	for _, h := range extraHeaders {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (s *sogouBingSource) searchSogou(ctx context.Context, query string, fetchCount int) ([]ImageResult, error) {
	u := "https://pic.sogou.com/pics?query=" + url.QueryEscape(query) + "&mode=6"
	body, err := s.fetchHTML(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("sogou fetch: %w", err)
	}
	const marker = "window.__INITIAL_STATE__="
	html := string(body)
	idx := strings.Index(html, marker)
	if idx == -1 {
		return nil, nil
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], "</script>")
	if end == -1 {
		return nil, nil
	}
	jsonStr := strings.TrimSpace(html[start : start+end])
	if i := strings.Index(jsonStr, ";(function"); i != -1 {
		jsonStr = jsonStr[:i]
	}
	var data struct {
		SearchList struct {
			SearchList []struct {
				PicUrl   string `json:"picUrl"`
				ThumbUrl string `json:"thumbUrl"`
				Width    int    `json:"width"`
				Height   int    `json:"height"`
				Title    string `json:"title"`
				Url      string `json:"url"`
				SiteName string `json:"ch_site_name"`
			} `json:"searchList"`
		} `json:"searchList"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil, nil
	}
	var out []ImageResult
	for _, item := range data.SearchList.SearchList {
		if len(out) >= fetchCount {
			break
		}
		imgURL := item.PicUrl
		if imgURL == "" {
			imgURL = item.ThumbUrl
		}
		if imgURL == "" {
			continue
		}
		src := domainOf(item.Url)
		if src == "" {
			src = item.SiteName
		}
		out = append(out, ImageResult{URL: imgURL, Source: src, Width: item.Width, Height: item.Height})
	}
	return out, nil
}

// ── Bing image scraper ────────────────────────────────────────────────────────

var bingMRe = regexp.MustCompile(`class="iusc"[^>]+m="([^"]+)"`)

func (s *sogouBingSource) searchBing(ctx context.Context, query string, fetchCount int) ([]ImageResult, error) {
	u := "https://www.bing.com/images/search?q=" + url.QueryEscape(query) + "&form=HDRSC3&first=1"
	body, err := s.fetchHTML(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("bing fetch: %w", err)
	}
	matches := bingMRe.FindAllSubmatch(body, -1)
	var out []ImageResult
	for _, m := range matches {
		if len(out) >= fetchCount {
			break
		}
		raw := strings.NewReplacer(`&quot;`, `"`, `&amp;`, `&`, `&#39;`, `'`).Replace(string(m[1]))
		var data struct {
			Murl string `json:"murl"`
			Turl string `json:"turl"`
			Purl string `json:"purl"`
			T    string `json:"t"`
			Mw   int    `json:"mw"`
			Mh   int    `json:"mh"`
		}
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}
		imgURL := data.Murl
		if imgURL == "" {
			imgURL = data.Turl
		}
		if imgURL == "" {
			continue
		}
		out = append(out, ImageResult{URL: imgURL, Source: domainOf(data.Purl), Width: data.Mw, Height: data.Mh})
	}
	return out, nil
}

// ── filter + dedup ────────────────────────────────────────────────────────────

var blockedDomains = []string{
	"pinterest.com", "pinimg.com", "tumblr.com", "blogspot.com", "wordpress.com",
	"sina.com.cn", "ifeng.com", "163.com", "toutiao.com", "thepaper.cn",
	"chinadaily.com.cn", "chinanews.com.cn", "zhihu.com", "gov.cn",
	"pptbz.com", "tukuppt.com", "51pptmoban.com",
}

var trustedDomains = []string{
	"unsplash.com", "pixabay.com", "zcool.com.cn", "nipic.com",
	"tuchong.com", "hellorf.com", "ooopic.com", "58pic.com", "588ku.com",
}

func isBlocked(src string) bool {
	for _, d := range blockedDomains {
		if strings.Contains(src, d) {
			return true
		}
	}
	return false
}

func isTrusted(src string) bool {
	for _, d := range trustedDomains {
		if strings.Contains(src, d) {
			return true
		}
	}
	return false
}

const minWidth, minHeight = 400, 300

func passesSize(r ImageResult) bool {
	if r.Width > 0 && r.Width < minWidth {
		return false
	}
	if r.Height > 0 && r.Height < minHeight {
		return false
	}
	return true
}

func dedup(images []ImageResult) []ImageResult {
	seen := map[string]bool{}
	out := images[:0:0]
	for _, img := range images {
		if img.URL == "" || seen[img.URL] {
			continue
		}
		seen[img.URL] = true
		out = append(out, img)
	}
	return out
}

func filterImages(images []ImageResult, query string, limit int) []ImageResult {
	terms := strings.Fields(strings.ToLower(query))
	relevant := func(img ImageResult) bool {
		if len(terms) == 0 {
			return true
		}
		hay := strings.ToLower(img.Source)
		for _, t := range terms {
			if len(t) > 1 && strings.Contains(hay, t) {
				return true
			}
		}
		return true // be permissive — relevance filter kept light
	}

	// three-pass: strict → relax relevance → size only
	candidates := images
	for _, filter := range []func(ImageResult) bool{
		func(r ImageResult) bool { return !isBlocked(r.Source) && relevant(r) && passesSize(r) },
		func(r ImageResult) bool { return !isBlocked(r.Source) && passesSize(r) },
		func(r ImageResult) bool { return passesSize(r) },
	} {
		var pass []ImageResult
		for _, img := range candidates {
			if filter(img) {
				pass = append(pass, img)
			}
		}
		if len(pass) >= limit {
			candidates = pass
			break
		}
		if len(pass) > 0 {
			candidates = pass
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		ti, tj := isTrusted(candidates[i].Source), isTrusted(candidates[j].Source)
		if ti != tj {
			return ti
		}
		return candidates[i].Width*candidates[i].Height > candidates[j].Width*candidates[j].Height
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

// ── SearchImages: dual-source parallel ───────────────────────────────────────

const fetchCount = 30

func (s *sogouBingSource) SearchImages(ctx context.Context, queryCN, queryEN string, limit int) ([]ImageResult, error) {
	if limit <= 0 {
		limit = 6
	}
	// Dual mode: parallel Sogou (CN) + Bing (EN).
	if queryCN != "" && queryEN != "" {
		type res struct {
			imgs []ImageResult
			err  error
		}
		sogouCh := make(chan res, 1)
		bingCh := make(chan res, 1)
		go func() {
			imgs, err := s.searchSogou(ctx, queryCN, fetchCount)
			sogouCh <- res{imgs, err}
		}()
		go func() {
			imgs, err := s.searchBing(ctx, queryEN, fetchCount)
			bingCh <- res{imgs, err}
		}()
		sr, br := <-sogouCh, <-bingCh
		// Bing first (more international quality sources), then Sogou.
		merged := dedup(append(append([]ImageResult{}, br.imgs...), sr.imgs...))
		if len(merged) == 0 {
			if br.err != nil {
				return nil, br.err
			}
			if sr.err != nil {
				return nil, sr.err
			}
			return nil, fmt.Errorf("no images found")
		}
		return filterImages(merged, queryCN+" "+queryEN, limit), nil
	}

	// Single-mode: auto-detect language for provider order.
	q := queryCN
	if q == "" {
		q = queryEN
	}
	isCN := false
	for _, r := range q {
		if r >= 0x4e00 && r <= 0x9fff {
			isCN = true
			break
		}
	}
	providers := []func(context.Context, string, int) ([]ImageResult, error){
		s.searchBing, s.searchSogou,
	}
	if isCN {
		providers = []func(context.Context, string, int) ([]ImageResult, error){
			s.searchSogou, s.searchBing,
		}
	}
	var last []ImageResult
	for _, fn := range providers {
		imgs, err := fn(ctx, q, fetchCount)
		if err != nil {
			continue
		}
		filtered := filterImages(imgs, q, limit)
		if len(filtered) > 0 {
			return filtered, nil
		}
		if len(imgs) > 0 {
			last = imgs
		}
	}
	if len(last) > 0 {
		return filterImages(last, q, limit), nil
	}
	return nil, fmt.Errorf("no images found for %q", q)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func domainOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// StubSource returns canned results — use in tests.
type StubSource struct {
	WebResults   []WebResult
	ImageResults []ImageResult
	Err          error
}

func (s *StubSource) SearchWeb(_ context.Context, _ string, _ int) ([]WebResult, error) {
	return s.WebResults, s.Err
}
func (s *StubSource) SearchImages(_ context.Context, _, _ string, _ int) ([]ImageResult, error) {
	return s.ImageResults, s.Err
}
