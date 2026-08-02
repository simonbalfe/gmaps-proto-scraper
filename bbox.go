package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

const mercatorLatitudeLimit = 85.05112878

type bounds struct {
	West  float64
	South float64
	East  float64
	North float64
}

type point struct {
	Latitude  float64
	Longitude float64
}

type polygon [][]point

type geoJSON struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
	Geometry    *geoJSON        `json:"geometry"`
	Features    []geoJSON       `json:"features"`
}

type scanStats struct {
	Tiles    int
	Requests int
}

type scanJob struct {
	Query  string
	Centre point
}

type scanResult struct {
	Places   []place
	Requests int
	Err      error
}

func parseBounds(value string) (bounds, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return bounds{}, errors.New("-bbox must be west,south,east,north")
	}
	numbers := make([]float64, 4)
	for i, part := range parts {
		number, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return bounds{}, fmt.Errorf("parse bbox value %q: %w", part, err)
		}
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return bounds{}, fmt.Errorf("bbox value %q must be finite", part)
		}
		numbers[i] = number
	}
	result := bounds{
		West:  numbers[0],
		South: numbers[1],
		East:  numbers[2],
		North: numbers[3],
	}
	if result.West < -180 || result.East > 180 || result.West >= result.East {
		return bounds{}, errors.New("bbox west/east must satisfy -180 <= west < east <= 180")
	}
	if result.South < -mercatorLatitudeLimit ||
		result.North > mercatorLatitudeLimit ||
		result.South >= result.North {
		return bounds{}, fmt.Errorf(
			"bbox south/north must satisfy %.8f <= south < north <= %.8f",
			-mercatorLatitudeLimit,
			mercatorLatitudeLimit,
		)
	}
	return result, nil
}

func parseGeoJSONArea(data []byte) ([]polygon, bounds, error) {
	var document geoJSON
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, bounds{}, fmt.Errorf("decode GeoJSON: %w", err)
	}
	polygons, err := geoJSONPolygons(document)
	if err != nil {
		return nil, bounds{}, err
	}
	if len(polygons) == 0 {
		return nil, bounds{}, errors.New("GeoJSON contains no polygons")
	}

	result := bounds{West: math.Inf(1), South: math.Inf(1), East: math.Inf(-1), North: math.Inf(-1)}
	for _, value := range polygons {
		for _, ring := range value {
			for _, position := range ring {
				result.West = min(result.West, position.Longitude)
				result.South = min(result.South, position.Latitude)
				result.East = max(result.East, position.Longitude)
				result.North = max(result.North, position.Latitude)
			}
		}
	}
	if result.West >= result.East || result.South >= result.North {
		return nil, bounds{}, errors.New("GeoJSON area must have non-zero width and height")
	}
	return polygons, result, nil
}

func geoJSONPolygons(value geoJSON) ([]polygon, error) {
	switch value.Type {
	case "FeatureCollection":
		var result []polygon
		for index, feature := range value.Features {
			polygons, err := geoJSONPolygons(feature)
			if err != nil {
				return nil, fmt.Errorf("feature %d: %w", index, err)
			}
			result = append(result, polygons...)
		}
		return result, nil
	case "Feature":
		if value.Geometry == nil {
			return nil, errors.New("feature has no geometry")
		}
		return geoJSONPolygons(*value.Geometry)
	case "Polygon":
		var coordinates [][][]float64
		if err := json.Unmarshal(value.Coordinates, &coordinates); err != nil {
			return nil, fmt.Errorf("decode Polygon coordinates: %w", err)
		}
		parsed, err := parsePolygon(coordinates)
		if err != nil {
			return nil, err
		}
		return []polygon{parsed}, nil
	case "MultiPolygon":
		var coordinates [][][][]float64
		if err := json.Unmarshal(value.Coordinates, &coordinates); err != nil {
			return nil, fmt.Errorf("decode MultiPolygon coordinates: %w", err)
		}
		result := make([]polygon, 0, len(coordinates))
		for index, coordinate := range coordinates {
			parsed, err := parsePolygon(coordinate)
			if err != nil {
				return nil, fmt.Errorf("polygon %d: %w", index, err)
			}
			result = append(result, parsed)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported GeoJSON type %q; use Polygon or MultiPolygon", value.Type)
	}
}

func parsePolygon(coordinates [][][]float64) (polygon, error) {
	if len(coordinates) == 0 {
		return nil, errors.New("polygon has no rings")
	}
	result := make(polygon, len(coordinates))
	for ringIndex, coordinates := range coordinates {
		if len(coordinates) < 4 {
			return nil, fmt.Errorf("ring %d must have at least four positions", ringIndex)
		}
		ring := make([]point, len(coordinates))
		for positionIndex, coordinates := range coordinates {
			if len(coordinates) < 2 {
				return nil, fmt.Errorf("ring %d position %d must contain longitude and latitude", ringIndex, positionIndex)
			}
			longitude, latitude := coordinates[0], coordinates[1]
			if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
				return nil, fmt.Errorf("ring %d position %d has invalid longitude", ringIndex, positionIndex)
			}
			if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -mercatorLatitudeLimit || latitude > mercatorLatitudeLimit {
				return nil, fmt.Errorf("ring %d position %d has invalid latitude", ringIndex, positionIndex)
			}
			ring[positionIndex] = point{Latitude: latitude, Longitude: longitude}
		}
		if ring[0] != ring[len(ring)-1] {
			return nil, fmt.Errorf("ring %d is not closed", ringIndex)
		}
		result[ringIndex] = ring
	}
	return result, nil
}

func (value bounds) contains(item place) bool {
	return item.Longitude >= value.West &&
		item.Longitude <= value.East &&
		item.Latitude >= value.South &&
		item.Latitude <= value.North
}

func scanBBox(searcher placeSearcher, opts appOptions) ([]place, scanStats, error) {
	centres := tileCentres(*opts.Bounds, opts.Zoom, opts.Width, opts.Height, opts.Overlap)
	if len(centres) > opts.MaxTiles {
		return nil, scanStats{}, fmt.Errorf(
			"bbox generated %d tiles, exceeding -max-tiles=%d",
			len(centres),
			opts.MaxTiles,
		)
	}

	jobs := make(chan scanJob)
	results := make(chan scanResult)
	workers := min(opts.Concurrency, len(centres)*len(opts.Queries))
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for job := range jobs {
				places, requests, err := scanTile(searcher, opts, job)
				results <- scanResult{Places: places, Requests: requests, Err: err}
			}
		}()
	}

	go func() {
		for _, query := range opts.Queries {
			for _, centre := range centres {
				jobs <- scanJob{Query: query, Centre: centre}
			}
		}
		close(jobs)
		waitGroup.Wait()
		close(results)
	}()

	unique := make(map[string]place)
	stats := scanStats{Tiles: len(centres)}
	var firstError error
	for result := range results {
		stats.Requests += result.Requests
		if result.Err != nil {
			if firstError == nil {
				firstError = result.Err
			}
			continue
		}
		for _, item := range result.Places {
			if opts.Bounds.contains(item) && (len(opts.Polygons) == 0 || polygonsContain(opts.Polygons, item)) {
				key := placeKey(item)
				unique[key] = mergePlace(unique[key], item)
			}
		}
	}
	if firstError != nil {
		return nil, stats, firstError
	}
	return mapPlaces(unique), stats, nil
}

func polygonsContain(polygons []polygon, item place) bool {
	position := point{Latitude: item.Latitude, Longitude: item.Longitude}
	for _, value := range polygons {
		if len(value) == 0 || !ringContains(value[0], position) {
			continue
		}
		inside := true
		for _, hole := range value[1:] {
			if ringContains(hole, position) {
				inside = false
				break
			}
		}
		if inside {
			return true
		}
	}
	return false
}

func ringContains(ring []point, position point) bool {
	inside := false
	for index := 0; index < len(ring)-1; index++ {
		first, second := ring[index], ring[index+1]
		if pointOnSegment(position, first, second) {
			return true
		}
		crosses := (first.Latitude > position.Latitude) != (second.Latitude > position.Latitude)
		if crosses && position.Longitude < (second.Longitude-first.Longitude)*
			(position.Latitude-first.Latitude)/(second.Latitude-first.Latitude)+first.Longitude {
			inside = !inside
		}
	}
	return inside
}

func pointOnSegment(position, first, second point) bool {
	const tolerance = 1e-10
	cross := (position.Latitude-first.Latitude)*(second.Longitude-first.Longitude) -
		(position.Longitude-first.Longitude)*(second.Latitude-first.Latitude)
	return math.Abs(cross) <= tolerance &&
		position.Longitude >= min(first.Longitude, second.Longitude)-tolerance &&
		position.Longitude <= max(first.Longitude, second.Longitude)+tolerance &&
		position.Latitude >= min(first.Latitude, second.Latitude)-tolerance &&
		position.Latitude <= max(first.Latitude, second.Latitude)+tolerance
}

func scanTile(searcher placeSearcher, opts appOptions, job scanJob) ([]place, int, error) {
	unique := make(map[string]place)
	requests := 0
	for page := range opts.MaxPages {
		request := singleRequest(
			opts,
			job.Query,
			job.Centre.Latitude,
			job.Centre.Longitude,
			page*resultCount,
		)
		places, err := searcher.search(request)
		requests++
		if err != nil {
			return nil, requests, fmt.Errorf(
				"query %q at %.6f,%.6f page %d: %w",
				job.Query,
				job.Centre.Latitude,
				job.Centre.Longitude,
				page+1,
				err,
			)
		}

		newPlaces := 0
		for _, item := range places {
			key := placeKey(item)
			if existing, exists := unique[key]; exists {
				unique[key] = mergePlace(existing, item)
				continue
			}
			unique[key] = item
			newPlaces++
		}
		if len(places) < resultCount || newPlaces == 0 {
			break
		}
	}
	return mapPlaces(unique), requests, nil
}

func tileCentres(value bounds, zoom float64, width, height int, overlap float64) []point {
	westX, northY := project(value.North, value.West, zoom)
	eastX, southY := project(value.South, value.East, zoom)
	spanX := eastX - westX
	spanY := southY - northY
	stepX := float64(width) * (1 - overlap)
	stepY := float64(height) * (1 - overlap)
	columns := max(1, int(math.Ceil(spanX/stepX)))
	rows := max(1, int(math.Ceil(spanY/stepY)))

	centres := make([]point, 0, columns*rows)
	for row := range rows {
		y := northY + (float64(row)+0.5)*spanY/float64(rows)
		for column := range columns {
			x := westX + (float64(column)+0.5)*spanX/float64(columns)
			latitude, longitude := unproject(x, y, zoom)
			centres = append(centres, point{Latitude: latitude, Longitude: longitude})
		}
	}
	return centres
}

func project(latitude, longitude, zoom float64) (float64, float64) {
	worldSize := tileSize * math.Pow(2, zoom)
	x := (longitude + 180) / 360 * worldSize
	sine := math.Sin(latitude * math.Pi / 180)
	y := (0.5 - math.Log((1+sine)/(1-sine))/(4*math.Pi)) * worldSize
	return x, y
}

func unproject(x, y, zoom float64) (float64, float64) {
	worldSize := tileSize * math.Pow(2, zoom)
	longitude := x/worldSize*360 - 180
	latitude := math.Atan(math.Sinh(math.Pi*(1-2*y/worldSize))) * 180 / math.Pi
	return latitude, longitude
}

func placeKey(item place) string {
	if item.PlaceID != "" {
		return "place:" + item.PlaceID
	}
	if item.CID != "" {
		return "cid:" + item.CID
	}
	if item.EntityID != "" {
		return "entity:" + item.EntityID
	}
	return fmt.Sprintf(
		"fallback:%s|%s|%.7f|%.7f",
		strings.ToLower(item.Name),
		strings.ToLower(item.Address),
		item.Latitude,
		item.Longitude,
	)
}

func mapPlaces(values map[string]place) []place {
	result := make([]place, 0, len(values))
	for _, item := range values {
		result = append(result, item)
	}
	return result
}

func mergePlace(current, candidate place) place {
	if current.Name == "" {
		return candidate
	}
	if current.Address == "" {
		current.Address = candidate.Address
	}
	if len(current.Categories) == 0 {
		current.Categories = candidate.Categories
	}
	if current.Latitude == 0 && current.Longitude == 0 {
		current.Latitude = candidate.Latitude
		current.Longitude = candidate.Longitude
	}
	if candidate.ReviewCount > current.ReviewCount {
		current.Rating = candidate.Rating
		current.ReviewCount = candidate.ReviewCount
	}
	if current.Phone == "" {
		current.Phone = candidate.Phone
	}
	if current.Website == "" {
		current.Website = candidate.Website
	}
	if current.PlaceID == "" {
		current.PlaceID = candidate.PlaceID
	}
	if current.CID == "" {
		current.CID = candidate.CID
	}
	if current.EntityID == "" {
		current.EntityID = candidate.EntityID
	}
	return current
}
