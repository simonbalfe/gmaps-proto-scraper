package main

import (
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	http "github.com/bogdanfinn/fhttp"
)

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z]{2,})+`)

type sitemapDocument struct {
	Sitemaps []string `xml:"sitemap>loc"`
	URLs     []string `xml:"url>loc"`
}

func websiteHeaders() http.Header {
	return http.Header{
		"Accept":          {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Accept-Language": {"en-GB,en;q=0.9"},
		"User-Agent":      {chromeUserAgent},
		http.HeaderOrderKey: {
			"accept",
			"accept-language",
			"user-agent",
		},
	}
}

func (client *googleClient) enrichEmails(places []place, extraPages, concurrency int) (int, []string) {
	if extraPages < 1 || concurrency < 1 {
		return 0, nil
	}
	jobs := make(chan int)
	var waitGroup sync.WaitGroup
	var mu sync.Mutex
	var warnings []string
	scanned := 0

	worker := func() {
		defer waitGroup.Done()
		for index := range jobs {
			if places[index].Website == "" {
				continue
			}
			emails, err := client.fetchWebsiteEmails(places[index].Website, extraPages)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s: %v", places[index].Name, err))
				mu.Unlock()
				continue
			}
			places[index].Emails = emails
			mu.Lock()
			scanned++
			mu.Unlock()
		}
	}

	workerCount := min(concurrency, len(places))
	waitGroup.Add(workerCount)
	for index := 0; index < workerCount; index++ {
		go worker()
	}
	for index := range places {
		jobs <- index
	}
	close(jobs)
	waitGroup.Wait()
	return scanned, warnings
}

func (client *googleClient) fetchWebsiteEmails(website string, extraPages int) ([]string, error) {
	homepage, err := homepageURL(website)
	if err != nil {
		return nil, err
	}
	body, err := client.get(homepage, websiteHeaders())
	if err != nil {
		return nil, fmt.Errorf("fetch homepage: %w", err)
	}
	emails := emailSet(string(body))

	sitemapURLs := client.discoverSitemaps(homepage)
	pageURLs := client.sitemapPageURLs(sitemapURLs, homepage, extraPages)
	for _, pageURL := range pageURLs {
		body, err := client.get(pageURL, websiteHeaders())
		if err != nil {
			continue
		}
		for email := range emailSet(string(body)) {
			emails[email] = struct{}{}
		}
	}

	result := make([]string, 0, len(emails))
	for email := range emails {
		result = append(result, email)
	}
	sort.Strings(result)
	return result, nil
}

func homepageURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid website URL %q", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported website URL scheme %q", parsed.Scheme)
	}
	parsed.Path = "/"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (client *googleClient) discoverSitemaps(homepage string) []string {
	parsed, err := url.Parse(homepage)
	if err != nil {
		return nil
	}
	root := *parsed
	root.Path = "/"
	root.RawQuery = ""
	root.Fragment = ""
	robots := root
	robots.Path = "/robots.txt"
	result := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		parsedURL, parseErr := url.Parse(value)
		if parseErr != nil || parsedURL.Hostname() != root.Hostname() {
			return
		}
		canonical := parsedURL.String()
		if _, exists := seen[canonical]; exists {
			return
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	if body, fetchErr := client.get(robots.String(), websiteHeaders()); fetchErr == nil {
		for _, line := range strings.Split(string(body), "\n") {
			key, value, found := strings.Cut(line, ":")
			if found && strings.EqualFold(strings.TrimSpace(key), "sitemap") {
				add(value)
			}
		}
	}
	sitemap := root
	sitemap.Path = "/sitemap.xml"
	add(sitemap.String())
	return result
}

func (client *googleClient) sitemapPageURLs(sitemapURLs []string, homepage string, limit int) []string {
	pages := make([]string, 0, limit)
	seenSitemaps := make(map[string]struct{})
	seenPages := map[string]struct{}{homepage: {}}
	queue := append([]string(nil), sitemapURLs...)
	for len(queue) > 0 && len(pages) < limit {
		current := queue[0]
		queue = queue[1:]
		if _, exists := seenSitemaps[current]; exists || len(seenSitemaps) >= 20 {
			continue
		}
		seenSitemaps[current] = struct{}{}
		body, err := client.get(current, websiteHeaders())
		if err != nil {
			continue
		}
		var document sitemapDocument
		if xml.Unmarshal(body, &document) != nil {
			continue
		}
		for _, child := range document.Sitemaps {
			if _, exists := seenSitemaps[strings.TrimSpace(child)]; !exists {
				queue = append(queue, strings.TrimSpace(child))
			}
		}
		for _, page := range document.URLs {
			page = strings.TrimSpace(page)
			if page == "" {
				continue
			}
			if _, exists := seenPages[page]; exists {
				continue
			}
			seenPages[page] = struct{}{}
			pages = append(pages, page)
			if len(pages) == limit {
				break
			}
		}
	}
	return pages
}

func emailSet(body string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, match := range emailPattern.FindAllString(html.UnescapeString(body), -1) {
		match = strings.ToLower(strings.TrimSpace(match))
		if isLikelyContactEmail(match) {
			result[match] = struct{}{}
		}
	}
	return result
}

func isLikelyContactEmail(value string) bool {
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	domain := parts[1]
	if domain == "sentry.io" || domain == "sentry.wixpress.com" ||
		strings.HasSuffix(domain, ".sentry.io") || strings.HasSuffix(domain, ".sentry.wixpress.com") {
		return false
	}
	for _, suffix := range []string{".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".webm", ".mp4", ".mov", ".css", ".js"} {
		if strings.HasSuffix(domain, suffix) {
			return false
		}
	}
	if parts[0] == "example" || strings.HasPrefix(parts[0], "example-") {
		return false
	}
	return true
}
