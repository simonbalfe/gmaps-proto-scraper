package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	earthRadiusMetres = 6371010.0
	tileSize          = 256.0
	fieldOfViewFactor = 27.3611
	resultCount       = 20
)

type searchRequest struct {
	Query     string
	Latitude  float64
	Longitude float64
	Zoom      float64
	Width     int
	Height    int
	Offset    int
	Language  string
	Country   string
}

type place struct {
	Name        string   `json:"name"`
	Address     string   `json:"address,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Latitude    float64  `json:"latitude,omitempty"`
	Longitude   float64  `json:"longitude,omitempty"`
	Rating      float64  `json:"rating,omitempty"`
	ReviewCount int      `json:"review_count,omitempty"`
	Phone       string   `json:"phone,omitempty"`
	PlaceID     string   `json:"place_id,omitempty"`
	CID         string   `json:"cid,omitempty"`
	EntityID    string   `json:"entity_id,omitempty"`
}

type placeSearcher interface {
	search(searchRequest) ([]place, error)
}

type googleClient struct {
	http       *http.Client
	limiter    *requestLimiter
	maxRetries int
}

type requestLimiter struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func newGoogleClient(opts appOptions) (*googleClient, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !opts.Direct {
		proxyURL, err := parseProxyURL(opts.ProxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		transport.DisableKeepAlives = true
		transport.MaxIdleConns = 0
		transport.MaxIdleConnsPerHost = -1
	}
	return &googleClient{
		http:       &http.Client{Transport: transport, Timeout: opts.Timeout},
		limiter:    &requestLimiter{interval: opts.RequestDelay},
		maxRetries: opts.Retries,
	}, nil
}

func parseProxyURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	proxyURL, err := url.Parse(value)
	if err != nil || proxyURL.Hostname() == "" {
		return nil, errors.New("proxy must be a valid HTTP, HTTPS, or SOCKS5 URL")
	}
	switch proxyURL.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy scheme must be http, https, socks5, or socks5h")
	}
	return proxyURL, nil
}

func (limiter *requestLimiter) wait() {
	if limiter == nil || limiter.interval == 0 {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	if now.Before(limiter.next) {
		time.Sleep(limiter.next.Sub(now))
		now = time.Now()
	}
	limiter.next = now.Add(limiter.interval)
}

func (client *googleClient) search(request searchRequest) ([]place, error) {
	body, err := client.fetch(request)
	if err != nil {
		return nil, err
	}
	return decodePlaces(body)
}

func (client *googleClient) fetch(request searchRequest) ([]byte, error) {
	requestURL := buildURL(request)
	for attempt := 0; attempt <= client.maxRetries; attempt++ {
		client.limiter.wait()
		resp, err := client.http.Get(requestURL)
		if err != nil {
			if attempt == client.maxRetries {
				return nil, fmt.Errorf("request Google Maps: %w", err)
			}
			time.Sleep(retryDelay(attempt, ""))
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}
		if resp.StatusCode == http.StatusOK {
			return body, nil
		}
		if !retryableStatus(resp.StatusCode) || attempt == client.maxRetries {
			return nil, fmt.Errorf("Google returned %s: %s", resp.Status, preview(body, 500))
		}
		time.Sleep(retryDelay(attempt, resp.Header.Get("Retry-After")))
	}
	return nil, errors.New("Google request retries exhausted")
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		return min(time.Duration(seconds)*time.Second, 30*time.Second)
	}
	if value, err := http.ParseTime(retryAfter); err == nil {
		return min(max(time.Until(value), time.Second), 30*time.Second)
	}
	return min(time.Second*time.Duration(1<<min(attempt, 4)), 15*time.Second)
}

func buildURL(request searchRequest) string {
	values := url.Values{}
	values.Set("gl", request.Country)
	values.Set("hl", request.Language)
	values.Set("pb", buildPB(request))
	values.Set("q", request.Query)
	values.Set("tbm", "map")
	return "https://www.google.com/search?" + values.Encode()
}

func buildPB(request searchRequest) string {
	altitude := fieldOfViewFactor * earthRadiusMetres * float64(request.Height) *
		math.Cos(request.Latitude*math.Pi/180) / (math.Pow(2, request.Zoom) * tileSize)
	pb := fmt.Sprintf(
		"!4m8!1m3!1d%.12f!2d%.12f!3d%.12f!3m2!1i%d!2i%d!4f13.1!7i%d",
		altitude,
		request.Longitude,
		request.Latitude,
		request.Width,
		request.Height,
		resultCount,
	)
	if request.Offset > 0 {
		pb += fmt.Sprintf("!8i%d", request.Offset)
	}
	return pb + "!10b1!34m1!31b1"
}

func decodePlaces(body []byte) ([]place, error) {
	text := strings.TrimPrefix(strings.TrimSpace(string(body)), ")]}'\n")
	var payload []any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("decode Maps payload: %w; prefix: %s", err, preview(body, 200))
	}
	if len(payload) <= 64 {
		return nil, fmt.Errorf("unexpected Maps payload: got %d top-level fields", len(payload))
	}

	rows, ok := payload[64].([]any)
	if !ok {
		return nil, errors.New("unexpected Maps payload: field 64 is not a result list")
	}

	places := make([]place, 0, len(rows))
	for _, rowValue := range rows {
		row, ok := rowValue.([]any)
		if !ok || len(row) < 2 {
			continue
		}
		record, ok := row[1].([]any)
		if !ok {
			continue
		}
		item := place{
			Name:       stringAt(record, 11),
			Address:    stringAt(record, 18),
			Categories: stringsAt(record, 13),
			CID:        stringAt(record, 10),
			PlaceID:    stringAt(record, 78),
			EntityID:   stringAt(record, 89),
		}
		if coordinates := arrayAt(record, 9); len(coordinates) >= 4 {
			item.Latitude = numberAt(coordinates, 2)
			item.Longitude = numberAt(coordinates, 3)
		}
		if reviews := arrayAt(record, 4); len(reviews) >= 8 {
			item.Rating = numberAt(reviews, 7)
			if len(reviews) >= 9 {
				item.ReviewCount = int(numberAt(reviews, 8))
			}
		}
		if phones := arrayAt(record, 178); len(phones) > 0 {
			if primary := arrayAt(phones, 0); len(primary) > 0 {
				item.Phone = stringAt(primary, 0)
			}
		}
		if item.Name != "" {
			places = append(places, item)
		}
	}
	return places, nil
}

func arrayAt(values []any, index int) []any {
	if index < 0 || index >= len(values) {
		return nil
	}
	value, _ := values[index].([]any)
	return value
}

func stringAt(values []any, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	value, _ := values[index].(string)
	return value
}

func stringsAt(values []any, index int) []string {
	items := arrayAt(values, index)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func numberAt(values []any, index int) float64 {
	if index < 0 || index >= len(values) {
		return 0
	}
	value, _ := values[index].(float64)
	return value
}

func preview(value []byte, limit int) string {
	text := strings.TrimSpace(string(value))
	if len(text) > limit {
		return text[:limit]
	}
	return text
}
