package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ", ")
}

func (values *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("query cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

type appOptions struct {
	Queries      []string
	ReviewsID    string
	ReviewLimit  int
	ReviewSort   string
	Latitude     float64
	Longitude    float64
	Bounds       *bounds
	Zoom         float64
	Width        int
	Height       int
	Offset       int
	Language     string
	Country      string
	Overlap      float64
	MaxPages     int
	MaxTiles     int
	Concurrency  int
	RequestDelay time.Duration
	Retries      int
	Direct       bool
	ProxyURL     string
	Raw          bool
	PrintURL     bool
	Verbose      bool
	Timeout      time.Duration
}

func main() {
	var queries stringList
	flag.Var(&queries, "query", "search phrase; repeat for multiple queries")
	reviewsID := flag.String("reviews-id", "", "Place ID, cid, or Google Maps URL to fetch reviews for")
	reviewLimit := flag.Int("review-limit", 100, "maximum reviews to return")
	reviewSort := flag.String("review-sort", "relevant", "review order: relevant, newest, highest, or lowest")
	latitude := flag.Float64("lat", math.NaN(), "map centre latitude")
	longitude := flag.Float64("lng", math.NaN(), "map centre longitude")
	bboxValue := flag.String("bbox", "", "west,south,east,north")
	zoom := flag.Float64("zoom", 14, "Google Maps zoom level")
	width := flag.Int("width", 1200, "map viewport width")
	height := flag.Int("height", 800, "map viewport height")
	offset := flag.Int("offset", 0, "single-search result offset in multiples of 20")
	language := flag.String("hl", "en", "interface language")
	country := flag.String("gl", "uk", "country code")
	overlap := flag.Float64("overlap", 0.2, "bbox tile overlap from 0 to less than 1")
	maxPages := flag.Int("max-pages", 6, "maximum result pages per query and tile")
	maxTiles := flag.Int("max-tiles", 500, "maximum generated bbox tiles")
	concurrency := flag.Int("concurrency", 4, "concurrent bbox tile searches")
	requestDelay := flag.Duration("request-delay", 100*time.Millisecond, "minimum delay between Google requests")
	retries := flag.Int("retries", 4, "retries for rate limits and server errors")
	direct := flag.Bool("direct", false, "disable the proxy")
	proxyURL := flag.String("proxy-url", os.Getenv("GMAPS_PROXY_URL"), "proxy URL; defaults to GMAPS_PROXY_URL")
	raw := flag.Bool("raw", false, "print the raw single-search response")
	printURL := flag.Bool("print-url", false, "print one single-search URL without sending it")
	verbose := flag.Bool("verbose", false, "print bbox scan progress to stderr")
	timeout := flag.Duration("timeout", 30*time.Second, "request timeout")
	flag.Parse()

	var scanBounds *bounds
	if strings.TrimSpace(*bboxValue) != "" {
		parsed, err := parseBounds(*bboxValue)
		if err != nil {
			exit(err)
		}
		scanBounds = &parsed
	}

	opts := appOptions{
		Queries:      queries,
		ReviewsID:    *reviewsID,
		ReviewLimit:  *reviewLimit,
		ReviewSort:   *reviewSort,
		Latitude:     *latitude,
		Longitude:    *longitude,
		Bounds:       scanBounds,
		Zoom:         *zoom,
		Width:        *width,
		Height:       *height,
		Offset:       *offset,
		Language:     *language,
		Country:      *country,
		Overlap:      *overlap,
		MaxPages:     *maxPages,
		MaxTiles:     *maxTiles,
		Concurrency:  *concurrency,
		RequestDelay: *requestDelay,
		Retries:      *retries,
		Direct:       *direct,
		ProxyURL:     *proxyURL,
		Raw:          *raw,
		PrintURL:     *printURL,
		Verbose:      *verbose,
		Timeout:      *timeout,
	}
	if err := run(opts); err != nil {
		exit(err)
	}
}

func run(opts appOptions) error {
	if err := validateAppOptions(opts); err != nil {
		return err
	}

	client, err := newGoogleClient(opts)
	if err != nil {
		return err
	}
	if opts.ReviewsID != "" {
		reviews, stats, err := client.reviews(opts.ReviewsID, opts.ReviewSort, opts.ReviewLimit)
		if err != nil {
			return err
		}
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "reviews=%d pages=%d complete=%t\n", len(reviews), stats.Pages, stats.Complete)
			for _, warning := range stats.Warnings {
				fmt.Fprintf(os.Stderr, "review note: %s\n", warning)
			}
		}
		return writeJSON(reviews)
	}
	if opts.Bounds != nil {
		places, stats, err := scanBBox(client, opts)
		if err != nil {
			return err
		}
		if opts.Verbose {
			fmt.Fprintf(
				os.Stderr,
				"tiles=%d queries=%d requests=%d unique_places=%d\n",
				stats.Tiles,
				len(opts.Queries),
				stats.Requests,
				len(places),
			)
		}
		return writePlaces(places)
	}

	if opts.Raw || opts.PrintURL {
		request := singleRequest(opts, opts.Queries[0], opts.Latitude, opts.Longitude, opts.Offset)
		if opts.PrintURL {
			fmt.Println(buildURL(request))
			return nil
		}
		body, err := client.fetch(request)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(body)
		return err
	}

	unique := make(map[string]place)
	for _, query := range opts.Queries {
		request := singleRequest(opts, query, opts.Latitude, opts.Longitude, opts.Offset)
		places, err := client.search(request)
		if err != nil {
			return err
		}
		for _, item := range places {
			key := placeKey(item)
			unique[key] = mergePlace(unique[key], item)
		}
	}
	return writePlaces(mapPlaces(unique))
}

func validateAppOptions(opts appOptions) error {
	if opts.Zoom < 0 || opts.Zoom > 22 {
		return errors.New("-zoom must be between 0 and 22")
	}
	if opts.Width < 1 || opts.Height < 1 {
		return errors.New("-width and -height must be positive")
	}
	if opts.Offset < 0 || opts.Offset%resultCount != 0 {
		return errors.New("-offset must be zero or a positive multiple of 20")
	}
	if opts.Timeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	if !opts.Direct && strings.TrimSpace(opts.ProxyURL) == "" {
		return errors.New("-proxy-url or GMAPS_PROXY_URL is required unless -direct is used")
	}
	if opts.ReviewsID != "" {
		if len(opts.Queries) != 0 || opts.Bounds != nil {
			return errors.New("-reviews-id cannot be combined with -query or -bbox")
		}
		if opts.Raw || opts.PrintURL {
			return errors.New("-raw and -print-url are not available with -reviews-id")
		}
		if opts.ReviewLimit < 1 {
			return errors.New("-review-limit must be positive")
		}
		_, err := parseReviewSort(opts.ReviewSort)
		return err
	}
	if len(opts.Queries) == 0 {
		return errors.New("at least one -query or -reviews-id is required")
	}
	if opts.Bounds == nil {
		if math.IsNaN(opts.Latitude) || opts.Latitude < -90 || opts.Latitude > 90 {
			return errors.New("-lat must be between -90 and 90")
		}
		if math.IsNaN(opts.Longitude) || opts.Longitude < -180 || opts.Longitude > 180 {
			return errors.New("-lng must be between -180 and 180")
		}
		if (opts.Raw || opts.PrintURL) && len(opts.Queries) != 1 {
			return errors.New("-raw and -print-url require exactly one -query")
		}
		return nil
	}
	if opts.Raw || opts.PrintURL {
		return errors.New("-raw and -print-url are only available without -bbox")
	}
	if opts.Offset != 0 {
		return errors.New("-offset is automatic in bbox mode")
	}
	if opts.Overlap < 0 || opts.Overlap >= 1 {
		return errors.New("-overlap must be at least 0 and less than 1")
	}
	if opts.MaxPages < 1 {
		return errors.New("-max-pages must be positive")
	}
	if opts.MaxTiles < 1 {
		return errors.New("-max-tiles must be positive")
	}
	if opts.Concurrency < 1 {
		return errors.New("-concurrency must be positive")
	}
	if opts.RequestDelay < 0 {
		return errors.New("-request-delay cannot be negative")
	}
	if opts.Retries < 0 {
		return errors.New("-retries cannot be negative")
	}
	return nil
}

func singleRequest(opts appOptions, query string, latitude, longitude float64, offset int) searchRequest {
	return searchRequest{
		Query:     query,
		Latitude:  latitude,
		Longitude: longitude,
		Zoom:      opts.Zoom,
		Width:     opts.Width,
		Height:    opts.Height,
		Offset:    offset,
		Language:  opts.Language,
		Country:   opts.Country,
	}
}

func writePlaces(places []place) error {
	sort.Slice(places, func(i, j int) bool {
		if places[i].Name == places[j].Name {
			return places[i].Address < places[j].Address
		}
		return places[i].Name < places[j].Name
	})
	return writeJSON(places)
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
