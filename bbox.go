package main

import (
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
			if opts.Bounds.contains(item) {
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
