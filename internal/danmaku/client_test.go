package danmaku

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSearchEpisodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/search/episodes" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("anime"); got != "孤独摇滚" {
			t.Fatalf("anime query = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"animes": []any{
				map[string]any{
					"animeId":    1,
					"animeTitle": "孤独摇滚！",
					"type":       "tvseries",
					"episodes": []any{
						map[string]any{"episodeId": 101, "episodeTitle": "第1话"},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	candidates, err := client.SearchEpisodes(context.Background(), "孤独摇滚")
	if err != nil {
		t.Fatalf("SearchEpisodes() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].AnimeID != 1 || candidates[0].Episodes[0].EpisodeID != 101 {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestClientCommentsAggregatesRelatedSources(t *testing.T) {
	var extCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/comment/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"comments": []any{
					map[string]any{"cid": 1, "p": "1.50,1,16777215,main", "m": "main"},
				},
			})
		case "/api/v2/related/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"relateds": []any{
					map[string]any{"url": "https://example.test/source"},
				},
			})
		case "/api/v2/extcomment":
			extCalls.Add(1)
			if got := r.URL.Query().Get("url"); got != "https://example.test/source" {
				t.Fatalf("extcomment url = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"comments": []any{
					map[string]any{"cid": 2, "p": "2.00,5,16711680,source", "m": "top"},
					map[string]any{"cid": 1, "p": "1.50,1,16777215,main", "m": "duplicate"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	result, err := client.Comments(context.Background(), 42)
	if err != nil {
		t.Fatalf("Comments() error = %v", err)
	}
	if result.Partial {
		t.Fatal("Partial = true, want false")
	}
	if result.Count != 2 || len(result.Comments) != 2 {
		t.Fatalf("count = %d, comments = %#v", result.Count, result.Comments)
	}
	if extCalls.Load() != 1 {
		t.Fatalf("ext calls = %d, want 1", extCalls.Load())
	}
	if result.Comments[0].Mode != 0 || result.Comments[0].Color != "#ffffff" {
		t.Fatalf("main comment = %#v", result.Comments[0])
	}
	if result.Comments[1].Mode != 1 || result.Comments[1].Color != "#ff0000" {
		t.Fatalf("related comment = %#v", result.Comments[1])
	}
}

func TestClientCommentsKeepsMainCommentsWhenRelatedFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/comment/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"comments": []any{
					map[string]any{"cid": 1, "p": "1,1,16777215,main", "m": "main"},
				},
			})
		case "/api/v2/related/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"relateds": []any{
					map[string]any{"url": "https://example.test/fail"},
				},
			})
		case "/api/v2/extcomment":
			http.Error(w, "upstream failed", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	result, err := client.Comments(context.Background(), 42)
	if err != nil {
		t.Fatalf("Comments() error = %v", err)
	}
	if !result.Partial {
		t.Fatal("Partial = false, want true")
	}
	if result.Count != 1 || result.Comments[0].Text != "main" {
		t.Fatalf("result = %#v", result)
	}
}

func TestClientCommentsIgnoresUnavailableRelatedDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/comment/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"comments": []any{
					map[string]any{"cid": 1, "p": "1,1,16777215,main", "m": "main"},
				},
			})
		case "/api/v2/related/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":      false,
				"errorCode":    4,
				"errorMessage": "application has no permission",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	result, err := client.Comments(context.Background(), 42)
	if err != nil {
		t.Fatalf("Comments() error = %v", err)
	}
	if result.Partial {
		t.Fatal("Partial = true, want false")
	}
	if result.Count != 1 || result.Comments[0].Text != "main" {
		t.Fatalf("result = %#v", result)
	}
}

func TestClientCommentsMainFailureReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "main failed", http.StatusBadGateway)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	if _, err := client.Comments(context.Background(), 42); err == nil {
		t.Fatal("Comments() error = nil")
	}
}

func TestClientCommentsRejectsUpstreamErrorPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":      false,
			"errorCode":    4,
			"errorMessage": "application has no permission",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	if _, err := client.Comments(context.Background(), 42); err == nil {
		t.Fatal("Comments() error = nil")
	}
}

func TestClientCommentsHonorsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "comments": []any{}})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Comments(ctx, 42)
	if err == nil {
		t.Fatal("Comments() error = nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
