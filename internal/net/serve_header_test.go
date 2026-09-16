package net

import (
	"net/http"
	"testing"
)

func TestProcessHeaderAllowlistIsOptIn(t *testing.T) {
	origin := http.Header{
		"Origin": {"https://browser.test"}, "Referer": {"https://browser.test/watch"},
		"Range": {"bytes=10-20"}, "If-Range": {`"etag"`}, "User-Agent": {"Browser"},
	}
	override := http.Header{"User-Agent": {"Mahiro Client/0.0.1"}}
	filtered := ProcessHeader(origin, override, []string{"Range", "If-Range"})
	if filtered.Get("Origin") != "" || filtered.Get("Referer") != "" {
		t.Fatalf("browser headers survived allowlist: %#v", filtered)
	}
	if filtered.Get("Range") != "bytes=10-20" || filtered.Get("If-Range") != `"etag"` ||
		filtered.Get("User-Agent") != "Mahiro Client/0.0.1" {
		t.Fatalf("required headers changed: %#v", filtered)
	}
	defaultHeaders := ProcessHeader(origin, override, nil)
	if defaultHeaders.Get("Origin") != origin.Get("Origin") || defaultHeaders.Get("Referer") != origin.Get("Referer") {
		t.Fatalf("default forwarding changed: %#v", defaultHeaders)
	}
	if origin.Get("User-Agent") != "Browser" {
		t.Fatal("processing mutated the inbound request")
	}
}
