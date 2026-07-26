package main

import (
	"strings"
	"testing"
)

func TestBuildPBUsesMinimalFields(t *testing.T) {
	pb := buildPB(searchRequest{
		Latitude:  51.5074,
		Longitude: -0.1278,
		Zoom:      14,
		Width:     1800,
		Height:    432,
	})

	required := []string{
		"!4m8",
		"!1m3!1d",
		"!2d-0.127800000000",
		"!3d51.507400000000",
		"!3m2!1i1800!2i432",
		"!4f13.1",
		"!7i20",
		"!10b1",
		"!34m1!31b1",
	}
	for _, value := range required {
		if !strings.Contains(pb, value) {
			t.Fatalf("expected %q in %q", value, pb)
		}
	}
	if strings.Contains(pb, "!8i") {
		t.Fatalf("first page should omit offset: %q", pb)
	}
	if len(pb) > 120 {
		t.Fatalf("pb unexpectedly large: %d characters", len(pb))
	}
}

func TestBuildPBAddsPaginationOffset(t *testing.T) {
	pb := buildPB(searchRequest{
		Latitude:  51.5155,
		Longitude: -0.0922,
		Zoom:      14,
		Width:     1800,
		Height:    432,
		Offset:    20,
	})

	if !strings.Contains(pb, "!7i20!8i20!10b1") {
		t.Fatalf("expected pagination fields in %q", pb)
	}
}
