package main

import (
	"net/http"
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

func TestNewGoogleClientDisablesProxyKeepAlives(t *testing.T) {
	client, err := newGoogleClient(appOptions{
		Timeout:      time.Second,
		RequestDelay: time.Millisecond,
		Retries:      2,
		ProxyURL:     "http://user:password@proxy.example.com:1000",
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected HTTP transport")
	}
	if !transport.DisableKeepAlives {
		t.Fatal("proxy keep-alives must be disabled for per-request rotation")
	}
	if transport.Proxy == nil {
		t.Fatal("expected proxy configuration")
	}
}
