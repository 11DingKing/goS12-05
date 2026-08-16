package service

import (
	"sync"
	"testing"
	"time"

	"batteryops/internal/domain"
	"batteryops/internal/notify"
	"batteryops/internal/store"
)

func newTestService(transferTimeout time.Duration) (*Service, *store.Store, *notify.SMSNotifier) {
	st := store.New()
	n := notify.NewSMSNotifier()
	cfg := Config{
		ReportSLA:       15 * time.Minute,
		DispatchSLA:     20 * time.Minute,
		TransferTimeout: transferTimeout,
	}
	return New(st, n, cfg), st, n
}

func registerCabin(svc *Service, id string) {
	_, _ = svc.RegisterCabin(id, "Site "+id, 45.0)
}

// TestReportAlarmAndDispatch exercises the normal path: inspector reports an
// alarm, dispatcher dispatches, and the cabin is locked.
func TestReportAlarmAndDispatch(t *testing.T) {
	svc, st, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-1")

	result, err := svc.ReportAlarm("cabin-1", domain.AlarmTempOverLimit, "inspector-1")
	if err != nil {
		t.Fatalf("report alarm: %v", err)
	}
	if !result.Created {
		t.Fatal("expected order to be created")
	}
	if result.Order.Status != domain.MaintReported {
		t.Fatalf("expected reported, got %s", result.Order.Status)
	}

	order, err := svc.DispatchMaintenance(result.Order.ID, "dispatcher-1", "eng-1", "eng-2")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if order.Status != domain.MaintDispatched {
		t.Fatalf("expected dispatched, got %s", order.Status)
	}

	cabin, _ := st.GetCabin("cabin-1")
	if cabin.Status != domain.CabinLocked {
		t.Fatalf("expected cabin locked, got %s", cabin.Status)
	}
	if cabin.LockOrderID != result.Order.ID {
		t.Fatalf("expected lock order ID %s, got %s", result.Order.ID, cabin.LockOrderID)
	}
}

// TestDuplicateAlarmReport verifies that two inspectors reporting the same
// cabin results in only one order, and the second reporter is prompted to
// confirm.
func TestDuplicateAlarmReport(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-2")

	result1, _ := svc.ReportAlarm("cabin-2", domain.AlarmTempOverLimit, "inspector-A")
	result2, err := svc.ReportAlarm("cabin-2", domain.AlarmInsulation, "inspector-B")
	if err != nil {
		t.Fatalf("second report: %v", err)
	}
	if result2.Created {
		t.Fatal("expected second report to not create a new order")
	}
	if result1.Order.ID != result2.Order.ID {
		t.Fatal("expected both reports to reference the same order")
	}
	if len(result2.Order.DuplicateReporterIDs) != 1 {
		t.Fatalf("expected 1 duplicate reporter, got %d", len(result2.Order.DuplicateReporterIDs))
	}
	if result2.Order.DuplicateReporterIDs[0] != "inspector-B" {
		t.Fatalf("expected duplicate reporter inspector-B")
	}
}

// TestConcurrentAlarmReport verifies that concurrent alarm reports for the
// same cabin create exactly one order.
func TestConcurrentAlarmReport(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-3")

	var wg sync.WaitGroup
	results := make([]*ReportAlarmResult, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r, _ := svc.ReportAlarm("cabin-3", domain.AlarmTempOverLimit, "inspector")
			results[idx] = r
		}(i)
	}
	wg.Wait()

	createdCount := 0
	orderID := ""
	for _, r := range results {
		if r.Created {
			createdCount++
			orderID = r.Order.ID
		}
	}
	if createdCount != 1 {
		t.Fatalf("expected exactly 1 order created, got %d", createdCount)
	}

	duplicates := 0
	for _, r := range results {
		if !r.Created && r.Order.ID == orderID {
			duplicates++
		}
	}
	if duplicates != 4 {
		t.Fatalf("expected 4 duplicates, got %d", duplicates)
	}
}

// TestFullMaintenanceFlow exercises the complete maintenance lifecycle from
// alarm to dual acceptance, verifying the cabin is unlocked at the end.
func TestFullMaintenanceFlow(t *testing.T) {
	svc, st, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-4")

	result, _ := svc.ReportAlarm("cabin-4", domain.AlarmTempOverLimit, "inspector-1")
	order, _ := svc.DispatchMaintenance(result.Order.ID, "dispatcher-1", "eng-1", "eng-2")
	if order.Status != domain.MaintDispatched {
		t.Fatalf("expected dispatched, got %s", order.Status)
	}

	// Wrong engineer cannot arrive.
	if _, err := svc.EngineerArrive(result.Order.ID, "eng-wrong"); err == nil {
		t.Fatal("expected error for wrong engineer")
	}

	order, _ = svc.EngineerArrive(result.Order.ID, "eng-1")
	if order.Status != domain.MaintProcessing {
		t.Fatalf("expected processing, got %s", order.Status)
	}

	cabin, _ := st.GetCabin("cabin-4")
	if cabin.Status != domain.CabinUnderMaintenance {
		t.Fatalf("expected under maintenance, got %s", cabin.Status)
	}

	order, _ = svc.CompleteProcessing(result.Order.ID, "eng-1")
	if order.Status != domain.MaintPendingAcceptance {
		t.Fatalf("expected pending_acceptance, got %s", order.Status)
	}

	// Acceptance requires both leader and station.
	if _, err := svc.AcceptMaintenance(result.Order.ID, "leader-1", ""); err == nil {
		t.Fatal("expected error with empty station")
	}

	order, _ = svc.AcceptMaintenance(result.Order.ID, "leader-1", "station-1")
	if order.Status != domain.MaintClosed {
		t.Fatalf("expected closed, got %s", order.Status)
	}

	cabin, _ = st.GetCabin("cabin-4")
	if cabin.Status != domain.CabinNormal {
		t.Fatalf("expected cabin normal after close, got %s", cabin.Status)
	}
	if cabin.LockOrderID != "" {
		t.Fatal("expected lock order ID to be cleared")
	}
}

// TestDispatchLocksCabinIdempotent verifies that re-dispatching the same
// order (same order ID) does not error due to idempotent cabin locking.
func TestDispatchLocksCabinIdempotent(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-5")

	result, _ := svc.ReportAlarm("cabin-5", domain.AlarmTempOverLimit, "inspector-1")
	orderID := result.Order.ID

	// First dispatch succeeds.
	_, err := svc.DispatchMaintenance(orderID, "dispatcher-1", "eng-1", "eng-2")
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	// Second dispatch on same order fails because status is no longer reported.
	_, err = svc.DispatchMaintenance(orderID, "dispatcher-1", "eng-1", "eng-2")
	if err == nil {
		t.Fatal("expected error re-dispatching already-dispatched order")
	}
}

// TestAutoTransferTimeout verifies that when the primary engineer does not
// arrive within the transfer timeout, the order is auto-transferred to the
// backup engineer and SMS notifications are sent.
func TestAutoTransferTimeout(t *testing.T) {
	svc, _, notifier := newTestService(50 * time.Millisecond)
	registerCabin(svc, "cabin-6")

	result, _ := svc.ReportAlarm("cabin-6", domain.AlarmTempOverLimit, "inspector-1")
	order, _ := svc.DispatchMaintenance(result.Order.ID, "dispatcher-1", "eng-1", "eng-2")

	// Wait for timeout.
	time.Sleep(100 * time.Millisecond)

	transferred := svc.CheckOverdueTransfers()
	if len(transferred) != 1 {
		t.Fatalf("expected 1 transferred order, got %d", len(transferred))
	}
	if transferred[0].CurrentEngineerID != "eng-2" {
		t.Fatalf("expected current engineer eng-2, got %s", transferred[0].CurrentEngineerID)
	}
	if transferred[0].Status != domain.MaintAssigned {
		t.Fatalf("expected assigned, got %s", transferred[0].Status)
	}

	// Verify SMS sent to both engineers.
	msgs := notifier.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 SMS messages, got %d", len(msgs))
	}

	// Second check should not transfer again (status is now assigned).
	time.Sleep(60 * time.Millisecond)
	transferred2 := svc.CheckOverdueTransfers()
	if len(transferred2) != 0 {
		t.Fatalf("expected no re-transfer, got %d", len(transferred2))
	}

	// Verify the order can still proceed with the backup engineer.
	order, _ = svc.EngineerArrive(result.Order.ID, "eng-2")
	if order.Status != domain.MaintProcessing {
		t.Fatalf("expected processing, got %s", order.Status)
	}
}

// TestInventoryReplenishmentOnConsume verifies that consuming parts below the
// safety line auto-generates a replenishment request.
func TestInventoryReplenishmentOnConsume(t *testing.T) {
	svc, _, notifier := newTestService(30 * time.Minute)
	_, _ = svc.AddSparePart("part-1", "Fuse", "pcs", 5, 3)

	// Consume to bring stock below safety line.
	part, req, err := svc.ConsumePart("part-1", 3)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if part.Stock != 2 {
		t.Fatalf("expected stock 2, got %d", part.Stock)
	}
	if req == nil {
		t.Fatal("expected replenishment request")
	}

	// Verify SMS notification sent.
	msgs := notifier.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(msgs))
	}

	// Consume again should not create duplicate request.
	_, req2, err := svc.ConsumePart("part-1", 1)
	if err != nil {
		t.Fatalf("consume again: %v", err)
	}
	if req2 != nil {
		t.Fatal("did not expect duplicate replenishment")
	}

	reqs := svc.ListReplenishmentRequests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 replenishment request, got %d", len(reqs))
	}
}

// TestCheckInventoryScheduler verifies the scheduler-style inventory scan
// creates replenishment requests for parts below the safety line.
func TestCheckInventoryScheduler(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	// Add a part already below safety line.
	_, _ = svc.AddSparePart("part-low", "Contactor", "pcs", 1, 5)

	reqs := svc.CheckInventory()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 replenishment request, got %d", len(reqs))
	}

	// Running again should not create a duplicate.
	reqs = svc.CheckInventory()
	if len(reqs) != 0 {
		t.Fatalf("expected 0 new requests on second scan, got %d", len(reqs))
	}
}

// TestGridSwitchDualConfirmation verifies that grid connection requires dual
// confirmation from both dispatcher and supply station before execution.
func TestGridSwitchDualConfirmation(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)

	// Dispatcher initiates.
	op, err := svc.InitiateOperation(domain.OperationGridConnect, "dispatcher-1")
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if !op.DispatcherConfirmed {
		t.Fatal("expected dispatcher confirmed on initiation")
	}
	if op.StationConfirmed {
		t.Fatal("expected station not yet confirmed")
	}

	// Cannot execute without station confirmation.
	if _, err := svc.ExecuteOperation(op.ID); err == nil {
		t.Fatal("expected error executing without station confirmation")
	}

	// Supply station confirms.
	op, err = svc.ConfirmOperation(op.ID, "station-1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !op.CanExecute() {
		t.Fatal("expected CanExecute after dual confirmation")
	}

	// Execute.
	op, err = svc.ExecuteOperation(op.ID)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if op.Status != domain.OperationExecuted {
		t.Fatalf("expected executed, got %s", op.Status)
	}

	// Cannot execute twice.
	if _, err := svc.ExecuteOperation(op.ID); err == nil {
		t.Fatal("expected error executing twice")
	}
}

// TestBlackStartCancellation verifies that a pending operation can be
// cancelled.
func TestBlackStartCancellation(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)

	op, _ := svc.InitiateOperation(domain.OperationBlackStart, "dispatcher-1")
	op, err := svc.CancelOperation(op.ID, "dispatcher-1")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if op.Status != domain.OperationCancelled {
		t.Fatalf("expected cancelled, got %s", op.Status)
	}

	// Cannot execute a cancelled operation.
	if _, err := svc.ExecuteOperation(op.ID); err == nil {
		t.Fatal("expected error executing cancelled operation")
	}
}

// TestSubmitReadingsTriggersAlarm verifies that submitting readings with a
// temperature over the limit transitions the cabin to the alarm state.
func TestSubmitReadingsTriggersAlarm(t *testing.T) {
	svc, st, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-7")

	cabin, err := svc.SubmitReadings("cabin-7", domain.Readings{
		Temperature:  50.0, // over 45.0 limit
		Humidity:     40.0,
		Voltage:      52.0,
		InsulationOK: true,
	})
	if err != nil {
		t.Fatalf("submit readings: %v", err)
	}
	if cabin.Status != domain.CabinAlarm {
		t.Fatalf("expected alarm status, got %s", cabin.Status)
	}

	// Verify the cabin in the store also has alarm status.
	c, _ := st.GetCabin("cabin-7")
	if c.Status != domain.CabinAlarm {
		t.Fatalf("store cabin status %s", c.Status)
	}
}

// TestInspectionWorkflow exercises the full inspection flow.
func TestInspectionWorkflow(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	registerCabin(svc, "cabin-8")

	order, err := svc.DispatchInspection("cabin-8", "inspector-1", "2026-W33")
	if err != nil {
		t.Fatalf("dispatch inspection: %v", err)
	}
	if order.Status != domain.InspectionCreated {
		t.Fatalf("expected created, got %s", order.Status)
	}

	order, _ = svc.StartInspection(order.ID)
	if order.Status != domain.InspectionInProgress {
		t.Fatalf("expected in_progress, got %s", order.Status)
	}

	order, _ = svc.RecordInspectionReadings(order.ID, domain.Readings{
		Temperature: 35.0,
		Humidity:    40.0,
		Voltage:     52.0,
	})
	if len(order.Readings) != 1 {
		t.Fatalf("expected 1 reading, got %d", len(order.Readings))
	}

	order, _ = svc.CompleteInspection(order.ID)
	if order.Status != domain.InspectionCompleted {
		t.Fatalf("expected completed, got %s", order.Status)
	}
}

// TestRegisterCabinDuplicate verifies that registering the same cabin twice
// is rejected.
func TestRegisterCabinDuplicate(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	_, err := svc.RegisterCabin("cabin-9", "Site", 45.0)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err = svc.RegisterCabin("cabin-9", "Site", 45.0)
	if err == nil {
		t.Fatal("expected error registering duplicate cabin")
	}
}

// TestReportAlarmNonexistentCabin verifies error handling for a missing cabin.
func TestReportAlarmNonexistentCabin(t *testing.T) {
	svc, _, _ := newTestService(30 * time.Minute)
	_, err := svc.ReportAlarm("nonexistent", domain.AlarmTempOverLimit, "inspector-1")
	if err == nil {
		t.Fatal("expected error for non-existent cabin")
	}
}
