package emby

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type responseBody struct {
	io.Reader
	closed bool
}

func (b *responseBody) Close() error {
	b.closed = true
	return nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestDecodeEmbyResponse(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		discard   bool
		readError bool
		wantError string
		wantCause bool
	}{
		{name: "JSON", status: 200, body: `{"Id":"item"}`},
		{name: "empty acknowledgement", status: 204, discard: true},
		{name: "non-JSON acknowledgement", status: 200, body: "OK", discard: true},
		{name: "invalid JSON", status: 200, body: "<html>", wantError: "invalid JSON response", wantCause: true},
		{name: "wrong field type", status: 200, body: `{"Id":1}`, wantError: "cannot unmarshal", wantCause: true},
		{name: "unauthorized", status: 401, body: "denied", wantError: "status=401"},
		{name: "discard still checks status", status: 403, discard: true, wantError: "status=403"},
		{name: "read error", status: 200, readError: true, wantError: "unexpected EOF", wantCause: true},
		{name: "read error before status", status: 401, readError: true, wantError: "unexpected EOF", wantCause: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &responseBody{Reader: strings.NewReader(test.body)}
			if test.readError {
				body.Reader = failingReader{}
			}
			resp := &http.Response{
				StatusCode: test.status,
				Header:     http.Header{"Content-Type": []string{" application/json "}},
				Body:       body,
			}
			var item embyItem
			var out any = &item
			if test.discard {
				out = nil
			}
			err := decodeEmbyResponse(resp, out, "test", "/Items")
			if !body.closed {
				t.Fatal("response body was not closed")
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !test.discard && item.ID != "item" {
					t.Fatalf("decoded item = %#v", item)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			if (err.cause != nil) != test.wantCause {
				t.Fatalf("cause = %v, want present = %t", err.cause, test.wantCause)
			}
			if err.action != "test" || err.target != "/Items" || err.contentType != "application/json" {
				t.Fatalf("response context lost: %#v", err)
			}
			if test.readError && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("read error not wrapped: %v", err)
			}
		})
	}
}

func TestRequestJSONResponseRetryPolicy(t *testing.T) {
	useFastEmbyRetries(t)
	for _, test := range []struct {
		name     string
		status   int
		body     string
		attempts int
	}{
		{name: "bad request", status: 400, body: `{}`, attempts: 1},
		{name: "unauthorized without credentials", status: 401, body: `{}`, attempts: 1},
		{name: "rate limited", status: 429, body: `{}`, attempts: embyMaxAttempts},
		{name: "server failure", status: 503, body: `{}`, attempts: embyMaxAttempts},
		{name: "invalid JSON", status: 200, body: `<html>`, attempts: embyMaxAttempts},
		{name: "wrong field type", status: 200, body: `{"Id":1}`, attempts: embyMaxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			var item embyItem
			err := newTestEmby(server).getJSON(context.Background(), "/Items", nil, &item, "test")
			if err == nil || attempts != test.attempts {
				t.Fatalf("error = %v, attempts = %d, want %d", err, attempts, test.attempts)
			}
		})
	}
}

func TestSelectMediaSourcePreservesPriority(t *testing.T) {
	for _, test := range []struct {
		name    string
		sources []embyMediaSource
		id      string
	}{
		{name: "empty"},
		{name: "skip blank IDs", sources: []embyMediaSource{{ID: " ", SupportsDirectStream: true}}},
		{name: "first fallback", sources: []embyMediaSource{{ID: " first ", Container: " mkv "}, {ID: "second", Container: "mp4"}}, id: "first"},
		{name: "first direct stream", sources: []embyMediaSource{{ID: "fallback"}, {ID: " direct ", Container: " mkv ", SupportsDirectStream: true}, {ID: "another", SupportsDirectStream: true}}, id: "direct"},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, container := selectMediaSource(test.sources)
			if id != test.id || (id != "" && container != "mkv") {
				t.Fatalf("selected = (%q, %q), want ID %q", id, container, test.id)
			}
		})
	}
}
