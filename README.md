# gmaps-proto-scraper

Build local-business lead lists from Google Maps by choosing what type of business you want and where to search.

Each lead can include:

- Business name
- Address, phone number, and website
- Categories
- Rating and review count
- Latitude and longitude
- Google Place ID and CID

Results are returned as JSON, deduplicated, and limited to the area you select.

## Setup

Install [Go](https://go.dev/) and provide a rotating HTTP, HTTPS, or SOCKS5 proxy:

```sh
export GMAPS_PROXY_URL="http://username:password@proxy.example.com:1000"
```

Google requests use `tls-client` with a Chrome 144 TLS and HTTP/2 fingerprint, matching User-Agent, and ordered browser headers. Proxy connections disable keep-alives so a rotating proxy can assign a new exit IP per request. Use `-direct` to run without a proxy.

## Get leads near a location

Search around central London and save the results:

```sh
go run . \
  -query dentists \
  -lat 51.5074 \
  -lng -0.1278 \
  > london-dentists.json
```

Replace `dentists` with any search you would use on Google Maps, such as `accountants`, `estate agents`, or `coffee shops`.

## Get leads across Greater London

For broader coverage, give the scraper a bounding box. It divides the area into smaller searches, follows result pages, removes duplicates, and excludes businesses outside the box.

```sh
go run . \
  -query dentists \
  -bbox=-0.5103,51.2868,0.3340,51.6919 \
  -zoom 14 \
  -max-pages 2 \
  -concurrency 16 \
  -verbose \
  > london-dentists.json
```

In one live Greater London benchmark, this search returned 1,976 unique dentist listings in 28 seconds. Speed and result counts vary with the query, proxy, and Google Maps.

To search an exact shape drawn in [geojson.io](https://geojson.io/next/), save it as GeoJSON and use `-bbox-file`. Polygon holes and MultiPolygon files are supported; results outside the drawn area are discarded.

```sh
go run . \
  -query restaurants \
  -bbox-file data/spain-provinces.geojson \
  -concurrency 25 \
  -request-delay 200ms \
  -max-pages 20 \
  -direct \
  > restaurantes-espana.json
```

## Combine several lead types

Repeat `-query` to build one combined list:

```sh
go run . \
  -query dentists \
  -query orthodontists \
  -query "dental implants" \
  -bbox=-0.5103,51.2868,0.3340,51.6919 \
  -zoom 14 \
  -max-pages 3 \
  -concurrency 12 \
  > london-dental-leads.json
```

## Get reviews for one lead

Reviews are fetched separately so the main lead search stays fast. Pass either the lead's `place_id`, its `cid`, or a Google Maps URL:

```sh
PLACE_ID=$(jq -r '.[0].place_id' london-dentists.json)

go run . \
  -reviews-id "$PLACE_ID" \
  -review-limit 100 \
  -review-sort newest \
  > london-dentist-reviews.json
```

Use `relevant`, `newest`, `highest`, or `lowest` for `-review-sort`. Each review can include the rating, text, publication time, reviewer, photos, review URL, and business response.

Add `-verbose` to see how many review pages were fetched and whether pagination completed.

## Find email addresses

Email lookup is optional. It scans each lead's homepage, then follows up to the requested number of sitemap pages and adds an `emails` array to each lead:

```sh
go run . \
  -query dentists \
  -bbox=-0.5103,51.2868,0.3340,51.6919 \
  -max-pages 2 \
  -email-pages 5 \
  -email-concurrency 8 \
  -verbose \
  > london-dentists-with-emails.json
```

`-email-pages 0` leaves email lookup disabled. The scraper checks `robots.txt`, common sitemap locations, sitemap indexes, and page URLs until the limit is reached. It keeps going when an individual website is unavailable.

## Export leads to CSV

The scraper returns JSON by default. With `jq` installed, convert the output into a spreadsheet-ready CSV:

```sh
go run . \
  -query dentists \
  -bbox=-0.5103,51.2868,0.3340,51.6919 \
  -zoom 14 \
  -max-pages 2 \
  -concurrency 16 \
  | jq -r '
      (["name","address","phone","website","categories","rating","reviews","latitude","longitude","place_id"] | @csv),
      (.[] | [
        .name,
        .address,
        .phone,
        .website,
        (.categories // [] | join("; ")),
        .rating,
        .review_count,
        .latitude,
        .longitude,
        .place_id
      ] | @csv)
    ' > london-dentists.csv
```

## Main options

| Option | Purpose |
| --- | --- |
| `-query` | Business type or search phrase. Repeat it to combine searches. |
| `-bbox` | Area to cover as `west,south,east,north`. |
| `-bbox-file` | GeoJSON Polygon or MultiPolygon area; cannot be combined with `-bbox`. |
| `-lat` and `-lng` | Centre point for one local search. |
| `-zoom` | Search-area granularity. Higher values create smaller searches and can improve coverage. |
| `-max-pages` | Number of result pages checked for each area. |
| `-concurrency` | Number of searches performed at the same time. |
| `-request-delay` | Minimum pause between requests, such as `100ms` or `1s`. |
| `-proxy-url` | Use a proxy without setting `GMAPS_PROXY_URL`. |
| `-direct` | Send requests directly instead of through a proxy. |
| `-verbose` | Show progress while keeping the saved JSON clean. |
| `-reviews-id` | Fetch reviews for one Place ID, cid, or Google Maps URL instead of searching for leads. |
| `-review-limit` | Maximum number of reviews to return. |
| `-review-sort` | Order reviews by relevance, date, highest rating, or lowest rating. |
| `-email-pages` | Scan the homepage plus this many sitemap pages for email addresses; `0` disables it. |
| `-email-concurrency` | Number of websites checked in parallel for email addresses. |

Google Maps ranks results rather than providing a complete business directory. Smaller search areas, overlapping coverage, and related query variations improve the number of leads found, but no search can guarantee every business.

The review retrieval approach was adapted from the MIT-licensed [google-maps-review-scraper](https://github.com/YasogaN/google-maps-review-scraper).
