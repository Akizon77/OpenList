package emby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
)

func useFastEmbyRetries(t *testing.T) {
	previous := embyRetryBaseDelay
	embyRetryBaseDelay = 0
	t.Cleanup(func() {
		embyRetryBaseDelay = previous
	})
}

func newTestEmby(server *httptest.Server) *Emby {
	driver := &Emby{
		Addition: Addition{URL: server.URL},
		client:   server.Client(),
	}
	driver.setAuth("test-token", "test-user")
	return driver
}

func TestGetItemsRetriesRateLimitAndInvalidJSON(t *testing.T) {
	useFastEmbyRetries(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, "<html>rate limited</html>")
		case 2:
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html>temporary gateway page</html>")
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"Items":[{"Name":"Episode","Id":"item-1","IsFolder":false}],"TotalRecordCount":1}`)
		}
	}))
	defer server.Close()

	items, err := newTestEmby(server).getItems(context.Background(), "parent-id")
	if err != nil {
		t.Fatalf("getItems() error = %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	if len(items) != 1 || items[0].ID != "item-1" {
		t.Fatalf("items = %#v, want item-1", items)
	}
}

func TestAuthenticateInvalidJSONErrorIncludesResponseDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html>login gateway</html>")
	}))
	defer server.Close()

	driver := &Emby{
		Addition: Addition{
			URL:      server.URL,
			Username: "test-user",
			Password: "test-password",
		},
		client: server.Client(),
	}
	_, _, err := driver.authenticate(context.Background())
	if err == nil {
		t.Fatal("authenticate() error = nil")
	}
	message := err.Error()
	for _, expected := range []string{
		"target=/Users/AuthenticateByName",
		"status=200",
		`content_type="text/html"`,
		`body="<html>login gateway</html>"`,
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q does not contain %q", message, expected)
		}
	}
	if strings.Contains(message, "test-password") {
		t.Fatalf("error leaks password: %q", message)
	}
}

func TestGetItemsRetriesFailedPageWithoutRestartingPagination(t *testing.T) {
	useFastEmbyRetries(t)
	firstPage := make([]embyItem, embyPageSize)
	for i := range firstPage {
		firstPage[i] = embyItem{ID: fmt.Sprintf("item-%d", i), Name: fmt.Sprintf("Item %d", i)}
	}
	var secondPageAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("StartIndex") == "0" {
			_ = json.NewEncoder(w).Encode(listResp{Items: firstPage, TotalRecordCount: intPointer(embyPageSize + 1)})
			return
		}
		if secondPageAttempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":"temporarily unavailable"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(listResp{
			Items:            []embyItem{{ID: "last-item", Name: "Last item"}},
			TotalRecordCount: intPointer(embyPageSize + 1),
		})
	}))
	defer server.Close()

	items, err := newTestEmby(server).getItems(context.Background(), "parent-id")
	if err != nil {
		t.Fatalf("getItems() error = %v", err)
	}
	if secondPageAttempts.Load() != 2 {
		t.Fatalf("second page attempts = %d, want 2", secondPageAttempts.Load())
	}
	if len(items) != embyPageSize+1 {
		t.Fatalf("items length = %d, want %d", len(items), embyPageSize+1)
	}
	if items[len(items)-1].ID != "last-item" {
		t.Fatalf("last item = %q, want last-item", items[len(items)-1].ID)
	}
}

func TestGetItemsUsesIndexRequestRateLimit(t *testing.T) {
	useFastEmbyRetries(t)
	previousConf := conf.Conf
	conf.Conf = conf.DefaultConfig("data")
	t.Cleanup(func() {
		conf.Conf = previousConf
	})
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	driver := newTestEmby(server)
	driver.client = internalnet.NewHttpClient()
	ctx := internalnet.WithRequestRateLimit(context.Background(), 1)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()

	_, err := driver.getItems(ctx, "parent-id")
	if err == nil {
		t.Fatal("getItems() error = nil")
	}
	if !errors.Is(err, internalnet.ErrRequestRateLimitWait) {
		t.Fatalf("getItems() error = %v, want request rate limit wait error", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("server attempts = %d, want 1 before the shared limiter blocked the retry", attempts.Load())
	}
}

func TestGetItemsInvalidJSONErrorIncludesRequestContext(t *testing.T) {
	useFastEmbyRetries(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html>bad gateway</html>")
	}))
	defer server.Close()

	_, err := newTestEmby(server).getItems(context.Background(), "parent-id")
	if err == nil {
		t.Fatal("getItems() error = nil")
	}
	if attempts.Load() != embyMaxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts.Load(), embyMaxAttempts)
	}
	message := err.Error()
	for _, expected := range []string{
		"ParentId=parent-id",
		"StartIndex=0",
		"status=200",
		`content_type="text/html"`,
		`body="<html>bad gateway</html>"`,
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q does not contain %q", message, expected)
		}
	}
	if strings.Contains(message, "api_key") || strings.Contains(message, "test-token") {
		t.Fatalf("error leaks credentials: %q", message)
	}
}

func TestEmbyRetryDelayHonorsRetryAfterAndCaps(t *testing.T) {
	previous := embyRetryBaseDelay
	embyRetryBaseDelay = time.Second
	t.Cleanup(func() {
		embyRetryBaseDelay = previous
	})

	if delay := embyRetryDelay(1, "2"); delay != 2*time.Second {
		t.Fatalf("embyRetryDelay() = %v, want 2s", delay)
	}
	if delay := embyRetryDelay(1, "120"); delay != embyMaxRetryDelay {
		t.Fatalf("embyRetryDelay() = %v, want cap %v", delay, embyMaxRetryDelay)
	}
}

func intPointer(value int) *int {
	return &value
}
