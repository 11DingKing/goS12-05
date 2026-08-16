package domain

import (
	"fmt"
	"time"
)

// CabinStatus represents the operational state of a battery cabin.
type CabinStatus string

const (
	CabinNormal           CabinStatus = "normal"
	CabinAlarm            CabinStatus = "alarm"
	CabinLocked           CabinStatus = "locked"
	CabinUnderMaintenance CabinStatus = "under_maintenance"
)

// Readings captures a single measurement snapshot submitted by an inspector.
type Readings struct {
	Temperature  float64   `json:"temperature"`
	Humidity     float64   `json:"humidity"`
	Voltage      float64   `json:"voltage"`
	InsulationOK bool      `json:"insulation_ok"`
	RecordedAt   time.Time `json:"recorded_at"`
}

// Cabin is an energy-storage battery cabin device.
type Cabin struct {
	ID          string      `json:"id"`
	Location    string      `json:"location"`
	Status      CabinStatus `json:"status"`
	LockOrderID string      `json:"lock_order_id,omitempty"`
	Readings    *Readings   `json:"readings,omitempty"`
	TempLimit   float64     `json:"temp_limit"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// NewCabin creates a battery cabin in the normal state.
func NewCabin(id, location string, tempLimit float64) *Cabin {
	return &Cabin{
		ID:        id,
		Location:  location,
		Status:    CabinNormal,
		TempLimit: tempLimit,
		UpdatedAt: time.Now(),
	}
}

// HasAlarmCondition returns true when the readings breach safe limits.
func (c *Cabin) HasAlarmCondition(r Readings) bool {
	return r.Temperature > c.TempLimit || !r.InsulationOK
}

// ApplyReadings records measurements and transitions to alarm if breached.
func (c *Cabin) ApplyReadings(r Readings) {
	now := time.Now()
	r.RecordedAt = now
	c.Readings = &r
	if c.HasAlarmCondition(r) && c.Status == CabinNormal {
		c.Status = CabinAlarm
	}
	c.UpdatedAt = now
}

// Lock attempts to transition the cabin into the locked state.
// Locking with the same order ID is idempotent.
func (c *Cabin) Lock(orderID string) error {
	if c.Status == CabinLocked {
		if c.LockOrderID == orderID {
			return nil
		}
		return fmt.Errorf("cabin %s already locked by order %s", c.ID, c.LockOrderID)
	}
	if c.Status == CabinUnderMaintenance {
		return fmt.Errorf("cabin %s is under maintenance", c.ID)
	}
	c.Status = CabinLocked
	c.LockOrderID = orderID
	c.UpdatedAt = time.Now()
	return nil
}

// Unlock returns the cabin to normal status.
func (c *Cabin) Unlock() {
	c.Status = CabinNormal
	c.LockOrderID = ""
	c.UpdatedAt = time.Now()
}

// StartMaintenance transitions from locked to under maintenance.
func (c *Cabin) StartMaintenance() error {
	if c.Status != CabinLocked {
		return fmt.Errorf("cabin %s must be locked before maintenance, current status: %s", c.ID, c.Status)
	}
	c.Status = CabinUnderMaintenance
	c.UpdatedAt = time.Now()
	return nil
}
