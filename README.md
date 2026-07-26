# gmaps-proto-scraper

Build local-business lead lists from Google Maps by choosing what type of business you want and where to search.

Each lead can include:

- Business name
- Address and phone number
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
      (["name","address","phone","categories","rating","reviews","latitude","longitude","place_id"] | @csv),
      (.[] | [
        .name,
        .address,
        .phone,
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
| `-lat` and `-lng` | Centre point for one local search. |
| `-zoom` | Search-area granularity. Higher values create smaller searches and can improve coverage. |
| `-max-pages` | Number of result pages checked for each area. |
| `-concurrency` | Number of searches performed at the same time. |
| `-request-delay` | Minimum pause between requests, such as `100ms` or `1s`. |
| `-proxy-url` | Use a proxy without setting `GMAPS_PROXY_URL`. |
| `-verbose` | Show progress while keeping the saved JSON clean. |

Google Maps ranks results rather than providing a complete business directory. Smaller search areas, overlapping coverage, and related query variations improve the number of leads found, but no search can guarantee every business.
