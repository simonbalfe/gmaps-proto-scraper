package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRetryableStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
	} {
		if !retryableStatus(status) {
			t.Fatalf("expected %d to be retryable", status)
		}
	}
	if retryableStatus(http.StatusBadRequest) {
		t.Fatal("bad requests should not be retried")
	}
}

func TestRetryDelayHonoursRetryAfter(t *testing.T) {
	if delay := retryDelay(0, "3"); delay != 3*time.Second {
		t.Fatalf("unexpected retry delay: %s", delay)
	}
	if delay := retryDelay(10, ""); delay != 15*time.Second {
		t.Fatalf("expected capped exponential delay, got %s", delay)
	}
}

func TestParseProxyURL(t *testing.T) {
	proxyURL, err := parseProxyURL("http://user:password@proxy.example.com:1000")
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL.Scheme != "http" ||
		proxyURL.Host != "proxy.example.com:1000" ||
		proxyURL.User.Username() != "user" {
		t.Fatalf("unexpected proxy URL: %s://%s", proxyURL.Scheme, proxyURL.Host)
	}
	password, ok := proxyURL.User.Password()
	if !ok || password != "password" {
		t.Fatal("proxy password was not preserved")
	}
}

func TestNewGoogleClientConfiguresTLSClientProxy(t *testing.T) {
	const proxyURL = "http://user:password@proxy.example.com:1000"
	client, err := newGoogleClient(appOptions{
		Timeout:      time.Second,
		RequestDelay: time.Millisecond,
		Retries:      2,
		ProxyURL:     proxyURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if actual := client.http.GetProxy(); actual != proxyURL {
		t.Fatalf("proxy = %q, expected %q", actual, proxyURL)
	}
}

func TestBrowserHeadersMatchTLSProfile(t *testing.T) {
	if actual := browserHeaders().Get("User-Agent"); !strings.Contains(actual, "Chrome/144.") {
		t.Fatalf("unexpected browser User-Agent: %q", actual)
	}
}

func TestNormaliseWebsite(t *testing.T) {
	tests := map[string]string{
		"https://example.com/contact?utm_source=google":                         "https://example.com/contact?utm_source=google",
		"https://example.com/search?q=dentists":                                 "https://example.com/search?q=dentists",
		"/url?q=https%3A%2F%2Fexample.com%2Fcontact%3Futm_source%3Dgoogle&sa=U": "https://example.com/contact?utm_source=google",
		"mailto:hello@example.com":                                              "",
		"":                                                                      "",
	}
	for input, expected := range tests {
		if actual := normaliseWebsite(input); actual != expected {
			t.Fatalf("normaliseWebsite(%q) = %q, expected %q", input, actual, expected)
		}
	}
}

func TestDecodePlacesExtractsWebsite(t *testing.T) {
	record := make([]any, 179)
	record[7] = []any{"/url?q=https%3A%2F%2Fexample.com%2Fcontact&sa=U"}
	record[11] = "Example Dental"
	payload := make([]any, 65)
	payload[64] = []any{[]any{nil, record}}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	places, err := decodePlaces(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 {
		t.Fatalf("expected one place, got %d", len(places))
	}
	if places[0].Website != "https://example.com/contact" {
		t.Fatalf("unexpected website: %q", places[0].Website)
	}
}
