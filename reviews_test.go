package main

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestBuildReviewsURL(t *testing.T) {
	requestURL, err := buildReviewsURL("0x123:0x456", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	var payload []any
	if err := json.Unmarshal([]byte(parsed.Query().Get("reqpld")), &payload); err != nil {
		t.Fatal(err)
	}
	inner := arrayAt(arrayAt(payload, 1), 9)
	if numberAt(inner, 1) != 2 {
		t.Fatalf("unexpected sort order: %v", inner[1])
	}
	if numberAt(inner, 9) != 10 {
		t.Fatalf("unexpected initial result count: %v", inner[9])
	}
	placeIDs := arrayAt(inner, 11)
	if stringAt(placeIDs, 0) != "0x123:0x456" {
		t.Fatalf("unexpected entity ID: %v", placeIDs)
	}

	requestURL, err = buildReviewsURL("0x123:0x456", 2, "next-page")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = url.Parse(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(parsed.Query().Get("reqpld")), &payload); err != nil {
		t.Fatal(err)
	}
	inner = arrayAt(arrayAt(payload, 1), 9)
	if stringAt(inner, 19) != "next-page" {
		t.Fatalf("unexpected pagination token: %v", inner[19])
	}
}

func TestDecodeReviewPage(t *testing.T) {
	rawReview := make([]any, 48)
	rawReview[1] = 5
	rawReview[2] = []any{"2 months ago", nil, "1779198716829"}
	rawReview[3] = []any{
		"Example Reviewer",
		"https://lh3.googleusercontent.com/avatar",
		"https://www.google.com/maps/contrib/123456789/reviews",
	}
	rawReview[4] = []any{nil, "1 month ago", "Thanks for your feedback", nil, ""}
	rawReview[5] = "review-1"
	rawReview[12] = "https://www.google.com/maps/reviews/data=review-1"
	rawReview[26] = "en"
	rawReview[27] = "The complete review text"
	rawReview[28] = "The short review text"
	rawReview[30] = []any{
		[]any{"https://lh3.googleusercontent.com/review-photo", nil, nil, "photo-1"},
	}
	rawReview[44] = []any{"Google"}

	node := make([]any, 7)
	node[2] = []any{rawReview}
	node[6] = "next-page"
	root := make([]any, 11)
	root[10] = node
	body, err := json.Marshal([]any{nil, root})
	if err != nil {
		t.Fatal(err)
	}
	body = append([]byte(")]}'\n"), body...)

	page, err := decodeReviewPage(body)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextToken != "next-page" {
		t.Fatalf("unexpected pagination token: %q", page.NextToken)
	}
	if len(page.Reviews) != 1 {
		t.Fatalf("expected one review, got %d", len(page.Reviews))
	}
	item := page.Reviews[0]
	if item.ReviewID != "review-1" ||
		item.Rating != 5 ||
		item.Text != "The complete review text" ||
		item.Language != "en" {
		t.Fatalf("unexpected review: %#v", item)
	}
	if item.PublishedAt != "2026-05-19T13:51:56Z" {
		t.Fatalf("unexpected published time: %q", item.PublishedAt)
	}
	if item.Author.ID != "123456789" || item.Author.Name != "Example Reviewer" {
		t.Fatalf("unexpected author: %#v", item.Author)
	}
	if item.OwnerResponse == nil || item.OwnerResponse.Text != "Thanks for your feedback" {
		t.Fatalf("unexpected owner response: %#v", item.OwnerResponse)
	}
	if len(item.Images) != 1 || item.Images[0].ID != "photo-1" {
		t.Fatalf("unexpected images: %#v", item.Images)
	}
}

func TestDecodeEmptyReviewPage(t *testing.T) {
	node := make([]any, 7)
	node[6] = ""
	root := make([]any, 11)
	root[10] = node
	body, err := json.Marshal([]any{nil, root})
	if err != nil {
		t.Fatal(err)
	}

	page, err := decodeReviewPage(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Reviews) != 0 || page.NextToken != "" {
		t.Fatalf("unexpected empty page: %#v", page)
	}
}

func TestFindReviewEntityID(t *testing.T) {
	tests := []string{
		"0x4876345498373701:0xb3029d85f045f154",
		"https://www.google.com/maps/place/example/data=!1s0x4876345498373701%3A0xb3029d85f045f154",
	}
	for _, value := range tests {
		if actual := findReviewEntityID(value); actual != "0x4876345498373701:0xb3029d85f045f154" {
			t.Fatalf("findReviewEntityID(%q) = %q", value, actual)
		}
	}
}

func TestValidateReviewMode(t *testing.T) {
	err := validateAppOptions(appOptions{
		ReviewsID:        "ChIJExample",
		ReviewLimit:      10,
		ReviewSort:       "newest",
		EmailConcurrency: 4,
		Zoom:             14,
		Width:            1200,
		Height:           800,
		Direct:           true,
		Timeout:          30,
	})
	if err != nil {
		t.Fatal(err)
	}
}
