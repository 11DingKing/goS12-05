package service

import (
	"errors"
	"testing"
	"time"

	"batteryops/internal/domain"
)

// TestConsumePartErrorClassification describes how callers are expected to tell
// inventory failures apart.
//
// Input: a spare part with 2 units on hand is asked to release 5 units; an
// unknown part id is asked to release 1 unit; and a known part is asked to
// release 0 units.
//
// Expected output: each failure can be classified with errors.Is against the
// exported inventory sentinels, and the message still carries the part id and
// the shortage figures.  A successful consumption returns no error.
func TestConsumePartErrorClassification(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	if _, err := svc.AddSparePart("part-bms", "BMS Controller", "pcs", 2, 3); err != nil {
		t.Fatalf("add part: %v", err)
	}

	_, _, err := svc.ConsumePart("part-bms", 5)
	if err == nil {
		t.Fatal("expected an error when consuming more than the stock on hand")
	}
	if !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("expected the shortage to be classifiable as domain.ErrInsufficientStock, got %#v (%v)", err, err)
	}
	if errors.Is(err, domain.ErrPartNotFound) || errors.Is(err, domain.ErrInvalidQuantity) {
		t.Fatalf("a stock shortage must not be classified as a missing part or a bad quantity: %v", err)
	}

	_, _, err = svc.ConsumePart("part-unknown", 1)
	if err == nil {
		t.Fatal("expected an error for an unknown part")
	}
	if !errors.Is(err, domain.ErrPartNotFound) {
		t.Fatalf("expected domain.ErrPartNotFound, got %#v (%v)", err, err)
	}

	_, _, err = svc.ConsumePart("part-bms", 0)
	if err == nil {
		t.Fatal("expected an error for a non-positive quantity")
	}
	if !errors.Is(err, domain.ErrInvalidQuantity) {
		t.Fatalf("expected domain.ErrInvalidQuantity, got %#v (%v)", err, err)
	}

	part, _, err := svc.ConsumePart("part-bms", 2)
	if err != nil {
		t.Fatalf("consuming the available stock must succeed: %v", err)
	}
	if part.Stock != 0 {
		t.Fatalf("expected stock 0, got %d", part.Stock)
	}
}
