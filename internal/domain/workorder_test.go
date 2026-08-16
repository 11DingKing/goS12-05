package domain

import (
	"testing"
	"time"
)

// TestMaintenanceOrderLifecycle exercises the full happy-path state machine:
// reported → dispatched → processing → pending_acceptance → closed.
func TestMaintenanceOrderLifecycle(t *testing.T) {
	o := NewMaintenanceOrder("MO-1", "cabin-1", AlarmTempOverLimit, "inspector-1")
	if o.Status != MaintReported {
		t.Fatalf("expected status reported, got %s", o.Status)
	}
	if !o.IsActive() {
		t.Fatal("new order should be active")
	}

	now := time.Now()
	o.ReportedAt = now

	if err := o.Dispatch("dispatcher-1", "eng-1", "eng-2"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if o.Status != MaintDispatched {
		t.Fatalf("expected dispatched, got %s", o.Status)
	}
	if o.CurrentEngineerID != "eng-1" {
		t.Fatalf("expected current engineer eng-1, got %s", o.CurrentEngineerID)
	}

	if err := o.EngineerArrive("eng-1"); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	if o.Status != MaintProcessing {
		t.Fatalf("expected processing, got %s", o.Status)
	}

	if err := o.CompleteProcessing("eng-1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if o.Status != MaintPendingAcceptance {
		t.Fatalf("expected pending_acceptance, got %s", o.Status)
	}

	if err := o.Accept("leader-1", "station-1"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if o.Status != MaintClosed {
		t.Fatalf("expected closed, got %s", o.Status)
	}
	if o.AcceptedByLeader != "leader-1" || o.AcceptedByStation != "station-1" {
		t.Fatal("acceptance fields not set")
	}
	if o.IsActive() {
		t.Fatal("closed order should not be active")
	}
}

// TestMaintenanceOrderInvalidTransitions verifies that illegal state
// transitions are rejected.
func TestMaintenanceOrderInvalidTransitions(t *testing.T) {
	o := NewMaintenanceOrder("MO-2", "cabin-2", AlarmInsulation, "inspector-2")

	// Cannot arrive before dispatch.
	if err := o.EngineerArrive("eng-1"); err == nil {
		t.Fatal("expected error arriving before dispatch")
	}

	if err := o.Dispatch("dispatcher-1", "eng-1", "eng-2"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Cannot dispatch twice.
	if err := o.Dispatch("dispatcher-1", "eng-1", "eng-2"); err == nil {
		t.Fatal("expected error dispatching twice")
	}

	// Wrong engineer cannot arrive.
	if err := o.EngineerArrive("eng-wrong"); err == nil {
		t.Fatal("expected error for wrong engineer")
	}

	if err := o.EngineerArrive("eng-1"); err != nil {
		t.Fatalf("arrive: %v", err)
	}

	// Cannot accept before completion.
	if err := o.Accept("leader-1", "station-1"); err == nil {
		t.Fatal("expected error accepting before completion")
	}

	if err := o.CompleteProcessing("eng-1"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Cannot accept with empty leader.
	if err := o.Accept("", "station-1"); err == nil {
		t.Fatal("expected error with empty leader")
	}

	// Close properly.
	if err := o.Accept("leader-1", "station-1"); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Cannot dispatch a closed order.
	if err := o.Dispatch("dispatcher-1", "eng-1", "eng-2"); err == nil {
		t.Fatal("expected error dispatching closed order")
	}
}

// TestMaintenanceOrderTransfer verifies that a dispatched order with a backup
// engineer can be transferred.
func TestMaintenanceOrderTransfer(t *testing.T) {
	o := NewMaintenanceOrder("MO-3", "cabin-3", AlarmTempOverLimit, "inspector-3")
	if err := o.Dispatch("dispatcher-1", "eng-1", "eng-2"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if err := o.Transfer(); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if o.Status != MaintAssigned {
		t.Fatalf("expected assigned, got %s", o.Status)
	}
	if o.CurrentEngineerID != "eng-2" {
		t.Fatalf("expected current engineer eng-2, got %s", o.CurrentEngineerID)
	}
	if o.TransferCount != 1 {
		t.Fatalf("expected transfer count 1, got %d", o.TransferCount)
	}

	// Backup engineer can now arrive.
	if err := o.EngineerArrive("eng-2"); err != nil {
		t.Fatalf("arrive after transfer: %v", err)
	}
}

// TestMaintenanceOrderTransferWithoutBackup verifies that transfer fails when
// no backup engineer is configured.
func TestMaintenanceOrderTransferWithoutBackup(t *testing.T) {
	o := NewMaintenanceOrder("MO-4", "cabin-4", AlarmInsulation, "inspector-4")
	if err := o.Dispatch("dispatcher-1", "eng-1", ""); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := o.Transfer(); err == nil {
		t.Fatal("expected error transferring without backup")
	}
}

// TestMaintenanceOrderIsTransferOverdue verifies the timeout logic.
func TestMaintenanceOrderIsTransferOverdue(t *testing.T) {
	o := NewMaintenanceOrder("MO-5", "cabin-5", AlarmTempOverLimit, "inspector-5")
	if err := o.Dispatch("dispatcher-1", "eng-1", "eng-2"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Simulate that 31 minutes have passed.
	past := time.Now().Add(-31 * time.Minute)
	o.DispatchedAt = &past

	if !o.IsTransferOverdue(time.Now(), 30*time.Minute) {
		t.Fatal("expected order to be overdue")
	}
	// Within timeout should not be overdue.
	fresh := time.Now().Add(-10 * time.Minute)
	o.DispatchedAt = &fresh
	if o.IsTransferOverdue(time.Now(), 30*time.Minute) {
		t.Fatal("expected order not to be overdue")
	}
}

// TestInspectionOrderLifecycle exercises the inspection order state machine.
func TestInspectionOrderLifecycle(t *testing.T) {
	o := NewInspectionOrder("IO-1", "cabin-1", "inspector-1", "2026-W33")
	if o.Status != InspectionCreated {
		t.Fatalf("expected created, got %s", o.Status)
	}

	if err := o.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if o.Status != InspectionInProgress {
		t.Fatalf("expected in_progress, got %s", o.Status)
	}

	r := Readings{Temperature: 35.0, Humidity: 40.0, Voltage: 52.0, InsulationOK: true}
	if err := o.RecordReadings(r); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(o.Readings) != 1 {
		t.Fatalf("expected 1 reading, got %d", len(o.Readings))
	}

	if err := o.Complete(); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if o.Status != InspectionCompleted {
		t.Fatalf("expected completed, got %s", o.Status)
	}

	// Completing twice is idempotent.
	if err := o.Complete(); err != nil {
		t.Fatalf("complete idempotent: %v", err)
	}

	// Cannot start a completed inspection.
	if err := o.Start(); err == nil {
		t.Fatal("expected error starting completed inspection")
	}
}
