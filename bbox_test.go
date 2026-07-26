package main

import (
	"sync"
	"testing"
	"time"
)

type fakeSearcher struct {
	mu        sync.Mutex
	requests  []searchRequest
	responses map[string][]place
}

func (searcher *fakeSearcher) search(request searchRequest) ([]place, error) {
	searcher.mu.Lock()
	searcher.requests = append(searcher.requests, request)
	searcher.mu.Unlock()
	return searcher.responses[request.Query], nil
}

func TestParseBounds(t *testing.T) {
	value, err := parseBounds("-0.5103,51.2868,0.3340,51.6919")
	if err != nil {
		t.Fatal(err)
	}
	if value.West != -0.5103 || value.South != 51.2868 ||
		value.East != 0.3340 || value.North != 51.6919 {
		t.Fatalf("unexpected bounds: %+v", value)
	}
}

func TestTileCentresCoversLondonWithMultipleTiles(t *testing.T) {
	value := bounds{West: -0.5103, South: 51.2868, East: 0.3340, North: 51.6919}
	centres := tileCentres(value, 14, 1200, 800, 0.2)
	if len(centres) < 2 {
		t.Fatalf("expected multiple tiles, got %d", len(centres))
	}
	for _, centre := range centres {
		if centre.Longitude < value.West || centre.Longitude > value.East ||
			centre.Latitude < value.South || centre.Latitude > value.North {
			t.Fatalf("centre outside bounds: %+v", centre)
		}
	}
}

func TestScanBBoxCombinesQueriesFiltersAndDeduplicates(t *testing.T) {
	searcher := &fakeSearcher{
		responses: map[string][]place{
			"barbers": {
				{Name: "Alpha", PlaceID: "alpha", Latitude: 51.50, Longitude: -0.10},
				{Name: "Outside", PlaceID: "outside", Latitude: 52.1, Longitude: -0.27},
			},
			"hairdressers": {
				{Name: "Alpha", PlaceID: "alpha", Latitude: 51.50, Longitude: -0.10},
				{Name: "Beta", CID: "beta", Latitude: 51.52, Longitude: -0.08},
			},
		},
	}
	value := bounds{West: -0.5103, South: 51.2868, East: 0.3340, North: 51.6919}
	opts := appOptions{
		Queries:     []string{"barbers", "hairdressers"},
		Bounds:      &value,
		Zoom:        12,
		Width:       5000,
		Height:      5000,
		Overlap:     0.2,
		MaxPages:    2,
		MaxTiles:    10,
		Concurrency: 2,
		Language:    "en",
		Country:     "uk",
		Timeout:     time.Second,
	}

	places, stats, err := scanBBox(searcher, opts)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Tiles != 1 || stats.Requests != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(places) != 2 {
		t.Fatalf("expected 2 unique in-bounds places, got %+v", places)
	}
}

func TestScanTileStopsWhenPaginationRepeats(t *testing.T) {
	repeated := make([]place, resultCount)
	for i := range repeated {
		repeated[i] = place{
			Name:      "Place",
			PlaceID:   "same",
			Latitude:  51.50,
			Longitude: -0.10,
		}
	}
	searcher := &fakeSearcher{responses: map[string][]place{"shops": repeated}}
	opts := appOptions{
		Queries:  []string{"shops"},
		Zoom:     15,
		Width:    1200,
		Height:   800,
		MaxPages: 6,
		Language: "en",
		Country:  "uk",
	}

	places, requests, err := scanTile(
		searcher,
		opts,
		scanJob{Query: "shops", Centre: point{Latitude: 51.50, Longitude: -0.10}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("expected repeated pagination to stop after 2 requests, got %d", requests)
	}
	if len(places) != 1 {
		t.Fatalf("expected one deduplicated place, got %d", len(places))
	}
}

func TestMergePlacePreservesWebsite(t *testing.T) {
	merged := mergePlace(
		place{Name: "Example Dental", PlaceID: "example"},
		place{Name: "Example Dental", PlaceID: "example", Website: "https://example.com"},
	)
	if merged.Website != "https://example.com" {
		t.Fatalf("unexpected website: %q", merged.Website)
	}
}
