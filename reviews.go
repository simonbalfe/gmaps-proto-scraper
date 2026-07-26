package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const reviewsEndpoint = "https://www.google.com/httpservice/web/PrivateLocalSearchUiDataService/GetLocalBoqProxy"

var reviewEntityPattern = regexp.MustCompile(`(?i)0x[0-9a-f]+:0x[0-9a-f]+`)

type review struct {
	ReviewID      string          `json:"review_id"`
	Rating        float64         `json:"rating"`
	Text          string          `json:"text,omitempty"`
	Language      string          `json:"language,omitempty"`
	Published     string          `json:"published,omitempty"`
	PublishedAt   string          `json:"published_at,omitempty"`
	URL           string          `json:"url,omitempty"`
	Author        reviewAuthor    `json:"author"`
	Images        []reviewImage   `json:"images,omitempty"`
	OwnerResponse *reviewResponse `json:"owner_response,omitempty"`
}

type reviewAuthor struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	ProfileURL string `json:"profile_url,omitempty"`
	PhotoURL   string `json:"photo_url,omitempty"`
}

type reviewImage struct {
	ID  string `json:"id,omitempty"`
	URL string `json:"url"`
}

type reviewResponse struct {
	Text        string `json:"text"`
	Published   string `json:"published,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type reviewPage struct {
	Reviews   []review
	NextToken string
}

type reviewStats struct {
	Pages    int
	Complete bool
	Warnings []string
}

func (client *googleClient) reviews(reviewID, sortOrder string, limit int) ([]review, reviewStats, error) {
	entityID, err := client.resolveReviewEntityID(reviewID)
	if err != nil {
		return nil, reviewStats{}, err
	}
	sortValue, err := parseReviewSort(sortOrder)
	if err != nil {
		return nil, reviewStats{}, err
	}

	results := make([]review, 0, limit)
	seen := make(map[string]struct{})
	nextToken := ""
	stats := reviewStats{Complete: true}
	for len(results) < limit {
		requestURL, err := buildReviewsURL(entityID, sortValue, nextToken)
		if err != nil {
			return nil, stats, err
		}
		page, err := client.fetchReviewPage(requestURL)
		if err != nil {
			if len(results) == 0 {
				return nil, stats, fmt.Errorf("fetch reviews: %w", err)
			}
			stats.Complete = false
			stats.Warnings = append(stats.Warnings, err.Error())
			break
		}
		stats.Pages++
		for _, item := range page.Reviews {
			key := item.ReviewID
			if key == "" {
				encoded, _ := json.Marshal(item)
				key = string(encoded)
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			results = append(results, item)
			if len(results) == limit {
				break
			}
		}
		if page.NextToken == "" || page.NextToken == nextToken || len(page.Reviews) == 0 {
			break
		}
		nextToken = page.NextToken
	}
	if len(results) >= limit {
		stats.Complete = false
		stats.Warnings = append(stats.Warnings, "review limit reached")
	}
	return results, stats, nil
}

func (client *googleClient) fetchReviewPage(requestURL string) (reviewPage, error) {
	var lastErr error
	for attempt := 0; attempt <= client.maxRetries; attempt++ {
		body, err := client.get(requestURL, browserHeaders())
		if err == nil {
			page, decodeErr := decodeReviewPage(body)
			if decodeErr == nil {
				return page, nil
			}
			lastErr = decodeErr
		} else {
			lastErr = err
		}
		if attempt < client.maxRetries {
			time.Sleep(retryDelay(attempt, ""))
		}
	}
	return reviewPage{}, lastErr
}

func (client *googleClient) resolveReviewEntityID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("-reviews-id cannot be empty")
	}
	if entityID := findReviewEntityID(value); entityID != "" {
		return entityID, nil
	}

	requestURL := ""
	if strings.HasPrefix(value, "ChIJ") {
		values := url.Values{}
		values.Set("api", "1")
		values.Set("query", "Google")
		values.Set("query_place_id", value)
		requestURL = "https://www.google.com/maps/search/?" + values.Encode()
	} else {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" || !isGoogleMapsHost(parsed.Hostname()) {
			return "", errors.New("-reviews-id must be a Place ID, cid, or Google Maps URL")
		}
		requestURL = parsed.String()
	}

	body, err := client.get(requestURL, browserHeaders())
	if err != nil {
		return "", fmt.Errorf("resolve review place: %w", err)
	}
	if entityID := findReviewEntityID(string(body)); entityID != "" {
		return entityID, nil
	}
	return "", errors.New("Google Maps did not return a review-compatible place identifier")
}

func findReviewEntityID(value string) string {
	for attempts := 0; attempts < 3; attempts++ {
		if match := reviewEntityPattern.FindString(value); match != "" {
			return strings.ToLower(match)
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil || decoded == value {
			break
		}
		value = decoded
	}
	return ""
}

func isGoogleMapsHost(host string) bool {
	host = strings.ToLower(host)
	return host == "google.com" ||
		strings.HasSuffix(host, ".google.com") ||
		host == "maps.app.goo.gl" ||
		host == "goo.gl"
}

func parseReviewSort(value string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "relevant":
		return 1, nil
	case "newest":
		return 2, nil
	case "highest", "highest_rating":
		return 3, nil
	case "lowest", "lowest_rating":
		return 4, nil
	default:
		return 0, errors.New("-review-sort must be relevant, newest, highest, or lowest")
	}
}

func buildReviewsURL(entityID string, sortOrder int, nextToken string) (string, error) {
	outer := make([]any, 10)
	inner := make([]any, 12)
	inner[1] = sortOrder
	inner[11] = []any{entityID}
	if nextToken == "" {
		inner[9] = 10
	} else {
		inner = append(inner, make([]any, 8)...)
		inner[19] = nextToken
	}
	outer[9] = inner
	payload, err := json.Marshal([]any{nil, outer})
	if err != nil {
		return "", fmt.Errorf("encode review request: %w", err)
	}
	values := url.Values{}
	values.Set("msc", "gwsrpc")
	values.Set("reqpld", string(payload))
	return reviewsEndpoint + "?" + values.Encode(), nil
}

func decodeReviewPage(body []byte) (reviewPage, error) {
	text := strings.TrimSpace(string(body))
	text = strings.TrimSpace(strings.TrimPrefix(text, ")]}'"))
	var payload []any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return reviewPage{}, fmt.Errorf("decode reviews payload: %w; prefix: %s", err, preview(body, 200))
	}
	root := arrayAt(payload, 1)
	node := arrayAt(root, 10)
	if len(node) < 3 {
		return reviewPage{}, errors.New("unexpected reviews payload")
	}
	if node[2] == nil {
		return reviewPage{Reviews: []review{}, NextToken: stringAt(node, 6)}, nil
	}
	rawReviews := arrayAt(node, 2)

	page := reviewPage{
		Reviews:   make([]review, 0, len(rawReviews)),
		NextToken: stringAt(node, 6),
	}
	for _, raw := range rawReviews {
		if parsed, ok := parseReview(raw); ok {
			page.Reviews = append(page.Reviews, parsed)
		}
	}
	return page, nil
}

func parseReview(value any) (review, bool) {
	raw, ok := value.([]any)
	if !ok || len(raw) < 6 {
		return review{}, false
	}

	item := review{
		ReviewID: stringAt(raw, 5),
		Rating:   numberAt(raw, 1),
		Author: reviewAuthor{
			Name: "A Google User",
		},
	}
	if published := arrayAt(raw, 2); published != nil {
		item.Published = stringAt(published, 0)
		item.PublishedAt = millisecondsAt(published, 2)
	}
	if author := arrayAt(raw, 3); author != nil {
		if name := stringAt(author, 0); name != "" {
			item.Author.Name = name
		}
		item.Author.PhotoURL = stringAt(author, 1)
		item.Author.ProfileURL = stringAt(author, 2)
		item.Author.ID = contributorID(item.Author.ProfileURL)
	}
	if response := arrayAt(raw, 4); response != nil {
		if text := stringAt(response, 2); text != "" {
			item.OwnerResponse = &reviewResponse{
				Text:        text,
				Published:   stringAt(response, 1),
				PublishedAt: millisecondsAt(response, 4),
			}
		}
	}

	googleIndex := lastGoogleIndex(raw)
	searchLimit := len(raw)
	if googleIndex >= 0 {
		searchLimit = googleIndex
	}
	item.Text, item.Language = reviewText(raw, searchLimit)
	item.URL = reviewURL(raw, searchLimit)
	item.Images = reviewImages(raw, searchLimit)
	return item, item.ReviewID != "" || item.Text != "" || item.Rating != 0
}

func contributorID(profileURL string) string {
	const marker = "/contrib/"
	start := strings.Index(profileURL, marker)
	if start < 0 {
		return ""
	}
	value := profileURL[start+len(marker):]
	if end := strings.IndexByte(value, '/'); end >= 0 {
		value = value[:end]
	}
	return value
}

func millisecondsAt(values []any, index int) string {
	value := stringAt(values, index)
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds <= 0 {
		return ""
	}
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339)
}

func lastGoogleIndex(values []any) int {
	for index := len(values) - 1; index >= 0; index-- {
		item := arrayAt(values, index)
		if stringAt(item, 0) == "Google" {
			return index
		}
	}
	return -1
}

func reviewText(values []any, limit int) (string, string) {
	for index := 6; index+2 < limit; index++ {
		language, ok := values[index].(string)
		if !ok || len(language) != 2 {
			continue
		}
		full, fullOK := values[index+1].(string)
		_, shortOK := values[index+2].(string)
		if fullOK && shortOK && full != "" {
			return full, language
		}
	}
	for index := 6; index < limit; index++ {
		text, ok := values[index].(string)
		if !ok || len(text) <= 5 || strings.HasPrefix(text, "http") || strings.HasPrefix(text, "//") {
			continue
		}
		return text, ""
	}
	return "", ""
}

func reviewURL(values []any, limit int) string {
	for index := 6; index < limit; index++ {
		value, ok := values[index].(string)
		if ok && strings.Contains(value, "google.com/maps/reviews/") {
			return value
		}
	}
	return ""
}

func reviewImages(values []any, limit int) []reviewImage {
	var images []reviewImage
	seen := make(map[string]struct{})
	for index := 6; index < limit; index++ {
		group := arrayAt(values, index)
		for _, candidate := range group {
			imageData, ok := candidate.([]any)
			if !ok {
				continue
			}
			imageURL := stringAt(imageData, 0)
			if !strings.Contains(imageURL, "googleusercontent") {
				continue
			}
			if _, exists := seen[imageURL]; exists {
				continue
			}
			seen[imageURL] = struct{}{}
			images = append(images, reviewImage{
				ID:  stringAt(imageData, 3),
				URL: imageURL,
			})
		}
	}
	return images
}
