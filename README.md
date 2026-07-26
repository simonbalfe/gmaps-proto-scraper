# gmaps-proto-scraper

A minimal Go client for Google Maps' private `www.google.com/search?tbm=map` endpoint.

It generates the required protobuf-shaped `pb` parameter from:

- Map centre latitude and longitude
- Zoom level
- Viewport dimensions
- Result count and pagination offset

It does not use a HAR, cookies, session identifiers, browser headers, or a copied Maps configuration blob.

Requests use a configurable proxy by default. Provide any standard HTTP, HTTPS, or SOCKS5 proxy URL:

```sh
export GMAPS_PROXY_URL="http://username:password@proxy.example.com:1000"
```

HTTP keep-alives are disabled so every Google request creates a new proxy connection. Providers that rotate IPs per connection will therefore rotate each request. Country targeting, session parameters, and provider-specific options belong in the proxy URL supplied by the user.

## Single search

```sh
go run . -query dentists -lat 51.5074 -lng -0.1278
```

Search a different location:

```sh
go run . -query "coffee shops" -lat 51.5155 -lng -0.0922 -zoom 14
```

Request the next 20 results:

```sh
go run . -query dentists -lat 51.5074 -lng -0.1278 -offset 20
```

Print the request URL:

```sh
go run . -query restaurants -lat 51.5074 -lng -0.1278 -print-url
```

## Bounding-box scan

Pass bounding coordinates as `west,south,east,north`:

```sh
go run . \
  -query dentists \
  -bbox=-0.5103,51.2868,0.3340,51.6919 \
  -zoom 14
```

The bbox is divided into overlapping Web Mercator viewports. Each viewport is paginated independently. Results are filtered back to the exact bbox and deduplicated by Place ID, CID, or entity ID.

Repeat `-query` to combine business categories:

```sh
go run . \
  -query restaurants \
  -query cafes \
  -query pubs \
  -query dentists \
  -query barbers \
  -query hairdressers \
  -query gyms \
  -query shops \
  -bbox=-0.5103,51.2868,0.3340,51.6919 \
  -zoom 14 \
  -width 1200 \
  -height 800 \
  -overlap 0.25 \
  -max-pages 3 \
  -concurrency 8 \
  -request-delay 100ms \
  -verbose
```

Useful bbox controls:

- `-zoom`: grid granularity. Higher values produce smaller geographic viewports.
- `-width` and `-height`: viewport dimensions used to calculate the grid.
- `-overlap`: minimum overlap between adjacent viewports.
- `-max-pages`: pagination limit for each query and viewport.
- `-max-tiles`: safety limit for generated viewports.
- `-concurrency`: simultaneous query and viewport jobs.
- `-request-delay`: global spacing between Google requests.
- `-retries`: retry count for HTTP 429 and server errors.
- `-proxy-url`: proxy URL, defaulting to `GMAPS_PROXY_URL`.
- `-direct`: bypass the configured proxy.
- `-verbose`: writes request statistics to stderr without corrupting JSON output.

The scanner stops paginating a viewport when Google returns fewer than 20 results or repeats the same result set.

## Essential request

The generated `pb` consists of:

```text
!4m8
  !1m3!1d<altitude>!2d<longitude>!3d<latitude>
  !3m2!1i<width>!2i<height>
  !4f13.1
!7i20
!8i<offset>
!10b1
!34m1!31b1
```

The offset field is omitted for the first page. Map altitude is calculated from zoom, latitude, and viewport height.

Google Maps returns ranked search results rather than an authoritative geographic database. Granular overlapping grids and repeated queries materially improve coverage, but cannot guarantee every business. Google Maps also uses a private endpoint and positional response schema, so these field identifiers and response indexes can change without notice.
