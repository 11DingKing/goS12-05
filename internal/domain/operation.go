package domain

import (
	"fmt"
	"time"
)

// OperationType classifies grid-switching operations requiring dual confirmation.
type OperationType string

const (
	OperationGridConnect OperationType = "grid_connect"
	OperationBlackStart  OperationType = "black_start"
)

// OperationStatus tracks the dual-confirmation lifecycle.
type OperationStatus string

const (
	OperationPending   OperationStatus = "pending_confirmation"
	OperationConfirmed OperationStatus = "confirmed"
	OperationExecuted  OperationStatus = "executed"
	OperationCancelled OperationStatus = "cancelled"
)

// Operation represents a grid connection or black start that requires
// dual confirmation from the dispatcher and the supply station.
type Operation struct {
	ID                  string          `json:"id"`
	Type                OperationType   `json:"type"`
	DispatcherID        string          `json:"dispatcher_id"`
	StationID           string          `json:"station_id,omitempty"`
	DispatcherConfirmed bool            `json:"dispatcher_confirmed"`
	StationConfirmed    bool            `json:"station_confirmed"`
	Status              OperationStatus `json:"status"`
	CreatedAt           time.Time       `json:"created_at"`
	ExecutedAt          *time.Time      `json:"executed_at,omitempty"`
	AuditTrail          []AuditEntry    `json:"audit_trail"`
}

// NewOperation creates a pending operation initiated by the dispatcher.
// The dispatcher's initiation counts as the first confirmation.
func NewOperation(id string, opType OperationType, dispatcherID string) *Operation {
	now := time.Now()
	return &Operation{
		ID:                  id,
		Type:                opType,
		DispatcherID:        dispatcherID,
		DispatcherConfirmed: true,
		Status:              OperationPending,
		CreatedAt:           now,
		AuditTrail: []AuditEntry{
			{Timestamp: now, Actor: dispatcherID, Action: "operation_initiated", Detail: string(opType)},
		},
	}
}

// ConfirmByStation records supply-station confirmation as the second leg of
// the dual confirmation. Confirming with the same station ID is idempotent.
func (o *Operation) ConfirmByStation(stationID string) error {
	if o.Status != OperationPending && o.Status != OperationConfirmed {
		return fmt.Errorf("cannot confirm operation %s in status %s", o.ID, o.Status)
	}
	if o.StationConfirmed {
		if o.StationID == stationID {
			return nil
		}
		return fmt.Errorf("operation %s already confirmed by station %s", o.ID, o.StationID)
	}
	o.StationID = stationID
	o.StationConfirmed = true
	o.Status = OperationConfirmed
	o.AuditTrail = append(o.AuditTrail, AuditEntry{
		Timestamp: time.Now(),
		Actor:     stationID,
		Action:    "station_confirmed",
	})
	return nil
}

// CanExecute returns true only when both parties have confirmed.
func (o *Operation) CanExecute() bool {
	return o.DispatcherConfirmed && o.StationConfirmed && o.Status == OperationConfirmed
}

// Execute transitions the operation to executed.
func (o *Operation) Execute() error {
	if !o.CanExecute() {
		return fmt.Errorf("operation %s requires dual confirmation before execution (dispatcher=%v station=%v)",
			o.ID, o.DispatcherConfirmed, o.StationConfirmed)
	}
	now := time.Now()
	o.Status = OperationExecuted
	o.ExecutedAt = &now
	o.AuditTrail = append(o.AuditTrail, AuditEntry{
		Timestamp: now,
		Actor:     "system",
		Action:    "operation_executed",
	})
	return nil
}

// Cancel aborts a pending or confirmed operation.
func (o *Operation) Cancel(actor string) error {
	if o.Status == OperationExecuted {
		return fmt.Errorf("cannot cancel executed operation %s", o.ID)
	}
	o.Status = OperationCancelled
	o.AuditTrail = append(o.AuditTrail, AuditEntry{
		Timestamp: time.Now(),
		Actor:     actor,
		Action:    "operation_cancelled",
	})
	return nil
}
