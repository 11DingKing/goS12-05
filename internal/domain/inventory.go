package domain

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors for inventory failures.  Callers such as the HTTP layer match
// on these with errors.Is instead of comparing message text, so that a spare
// part shortage can be reported differently from a malformed request.
var (
	// ErrPartNotFound is reported when the requested spare part is unknown.
	ErrPartNotFound = errors.New("spare part not found")
	// ErrInvalidQuantity is reported for a non-positive consume quantity.
	ErrInvalidQuantity = errors.New("consume quantity must be positive")
	// ErrInsufficientStock is reported when the requested quantity exceeds the
	// stock on hand.
	ErrInsufficientStock = errors.New("insufficient stock")
)

// SparePart is a replaceable component kept in inventory.
type SparePart struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Stock      int       `json:"stock"`
	SafetyLine int       `json:"safety_line"`
	Unit       string    `json:"unit"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NewSparePart creates a new spare part entry.
func NewSparePart(id, name, unit string, stock, safetyLine int) *SparePart {
	return &SparePart{
		ID:         id,
		Name:       name,
		Stock:      stock,
		SafetyLine: safetyLine,
		Unit:       unit,
		UpdatedAt:  time.Now(),
	}
}

// Consume reduces stock by the given quantity.
func (p *SparePart) Consume(qty int) error {
	if qty <= 0 {
		return fmt.Errorf("part %s: %w", p.ID, ErrInvalidQuantity)
	}
	if p.Stock < qty {
		return fmt.Errorf("part %s: have %d, need %d: %w", p.ID, p.Stock, qty, ErrInsufficientStock)
	}
	p.Stock -= qty
	p.UpdatedAt = time.Now()
	return nil
}

// BelowSafetyLine reports whether replenishment is needed.
func (p *SparePart) BelowSafetyLine() bool {
	return p.Stock < p.SafetyLine
}

// ReplenishmentStatus tracks the state of a restock request.
type ReplenishmentStatus string

const (
	ReplenishmentPending   ReplenishmentStatus = "pending"
	ReplenishmentFulfilled ReplenishmentStatus = "fulfilled"
)

// ReplenishmentRequest is an auto-generated restock order.
type ReplenishmentRequest struct {
	ID        string              `json:"id"`
	PartID    string              `json:"part_id"`
	Quantity  int                 `json:"quantity"`
	Status    ReplenishmentStatus `json:"status"`
	CreatedAt time.Time           `json:"created_at"`
}

// NewReplenishmentRequest creates a pending replenishment request.
func NewReplenishmentRequest(id, partID string, quantity int) *ReplenishmentRequest {
	return &ReplenishmentRequest{
		ID:        id,
		PartID:    partID,
		Quantity:  quantity,
		Status:    ReplenishmentPending,
		CreatedAt: time.Now(),
	}
}
