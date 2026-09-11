package danmaku

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout     = 15 * time.Second
	maxResponseBytes   = 64 << 20
	relatedConcurrency = 4
)

type apiResponse struct {
	Success      *bool  `json:"success"`
	ErrorCode    int    `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

type searchResponse struct {
	apiResponse
	Animes []animeResponse `json:"animes"`
}

type animeResponse struct {
	AnimeID         int64             `json:"animeId"`
	AnimeTitle      string            `json:"animeTitle"`
	Type            string            `json:"type"`
	TypeDescription string            `json:"typeDescription"`
	Episodes        []episodeResponse `json:"episodes"`
}

type episodeResponse struct {
	EpisodeID    int64  `json:"episodeId"`
	EpisodeTitle string `json:"episodeTitle"`
}

type commentResponse struct {
	apiResponse
	Count    int               `json:"count"`
	Comments []Comment         `json:"comments"`
	Relateds []json.RawMessage `json:"relateds"`
}

type Client struct {
	baseURL string
	client  *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid danmaku api url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid danmaku api url scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid danmaku api url host")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	} else {
		copied := *httpClient
		if copied.Timeout <= 0 || copied.Timeout > defaultTimeout {
			copied.Timeout = defaultTimeout
		}
		httpClient = &copied
	}
	return &Client{baseURL: baseURL, client: httpClient}, nil
}

func (c *Client) SearchEpisodes(ctx context.Context, query string) ([]AnimeCandidate, error) {
	var result searchResponse
	params := url.Values{}
	params.Set("anime", query)
	if err := c.getJSON(ctx, "/api/v2/search/episodes", params, &result); err != nil {
		return nil, err
	}
	candidates := make([]AnimeCandidate, 0, len(result.Animes))
	for _, anime := range result.Animes {
		candidate := AnimeCandidate{
			AnimeID:         anime.AnimeID,
			AnimeTitle:      anime.AnimeTitle,
			Type:            anime.Type,
			TypeDescription: anime.TypeDescription,
			Episodes:        make([]EpisodeCandidate, 0, len(anime.Episodes)),
		}
		for _, episode := range anime.Episodes {
			candidate.Episodes = append(candidate.Episodes, EpisodeCandidate{
				EpisodeID:    episode.EpisodeID,
				EpisodeTitle: episode.EpisodeTitle,
			})
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (c *Client) Comments(ctx context.Context, episodeID int64) (CommentsResult, error) {
	if episodeID <= 0 {
		return CommentsResult{}, fmt.Errorf("invalid episode id")
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	params := url.Values{}
	params.Set("withRelated", "true")
	params.Set("chConvert", "0")
	var main commentResponse
	if err := c.getJSON(ctx, "/api/v2/comment/"+strconv.FormatInt(episodeID, 10), params, &main); err != nil {
		return CommentsResult{}, err
	}

	comments := append([]Comment(nil), main.Comments...)
	partial := false

	related := append([]json.RawMessage(nil), main.Relateds...)
	if len(related) == 0 {
		var relatedResponse commentResponse
		if err := c.getJSON(ctx, "/api/v2/related/"+strconv.FormatInt(episodeID, 10), nil, &relatedResponse); err == nil {
			related = relatedResponse.Relateds
		}
	}

	sources := decodeRelatedSources(related)
	if len(sources) > 0 {
		results := make([][]Comment, len(sources))
		semaphore := make(chan struct{}, relatedConcurrency)
		var wg sync.WaitGroup
		var mu sync.Mutex
		for i, source := range sources {
			i, source := i, source
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					mu.Lock()
					partial = true
					mu.Unlock()
					return
				}

				params := url.Values{}
				params.Set("chConvert", "0")
				params.Set("url", source)
				var response commentResponse
				if err := c.getJSON(ctx, "/api/v2/extcomment", params, &response); err != nil {
					mu.Lock()
					partial = true
					mu.Unlock()
					return
				}
				results[i] = response.Comments
			}()
		}
		wg.Wait()
		for _, result := range results {
			comments = append(comments, result...)
		}
	}

	converted := convertComments(comments)
	return CommentsResult{
		Comments: converted,
		Count:    len(converted),
		Partial:  partial,
	}, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, params url.Values, out any) error {
	requestCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	requestURL, err := c.endpointURL(endpoint, params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "OpenList")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("danmaku upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read danmaku upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("danmaku upstream returned status %d: %s", resp.StatusCode, responseSnippet(body))
	}
	if len(body) == maxResponseBytes {
		return fmt.Errorf("danmaku upstream response is too large")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode danmaku upstream response: %w", err)
	}
	if response, ok := out.(interface{ apiStatus() apiResponse }); ok {
		status := response.apiStatus()
		if status.Success != nil && !*status.Success {
			return fmt.Errorf("danmaku upstream error %d: %s", status.ErrorCode, status.ErrorMessage)
		}
	}
	return nil
}

func (c *Client) endpointURL(endpoint string, params url.Values) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	base.Path = path.Join(base.Path, endpoint)
	base.RawPath = ""
	base.RawQuery = params.Encode()
	return base.String(), nil
}

func responseSnippet(body []byte) string {
	const limit = 512
	snippet := strings.TrimSpace(string(body))
	if len(snippet) <= limit {
		return snippet
	}
	return snippet[:limit]
}

func decodeRelatedSources(raws []json.RawMessage) []string {
	var sources []string
	seen := make(map[string]struct{})
	for _, raw := range raws {
		var source string
		if err := json.Unmarshal(raw, &source); err != nil {
			var object struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(raw, &object); err != nil {
				continue
			}
			source = object.URL
		}
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	return sources
}

func convertComments(raw []Comment) []Danmu {
	seen := make(map[string]struct{}, len(raw))
	result := make([]Danmu, 0, len(raw))
	for _, comment := range raw {
		parts := strings.Split(comment.P, ",")
		if len(parts) < 3 {
			continue
		}
		timestamp, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil || timestamp < 0 {
			continue
		}
		modeID, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		colorValue, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil || colorValue < 0 || colorValue > 0xffffff {
			colorValue = 0xffffff
		}
		text := strings.TrimSpace(comment.M)
		if text == "" {
			continue
		}
		key := strconv.FormatInt(comment.CID, 10)
		if comment.CID == 0 {
			key = comment.P + "\x00" + comment.M
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, Danmu{
			Text:  text,
			Mode:  converterMode(modeID),
			Color: fmt.Sprintf("#%06x", colorValue),
			Time:  timestamp,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Time < result[j].Time
	})
	return result
}

func converterMode(modeID int) int {
	switch modeID {
	case 4:
		return 2
	case 5:
		return 1
	default:
		return 0
	}
}

func (r apiResponse) apiStatus() apiResponse {
	return r
}
