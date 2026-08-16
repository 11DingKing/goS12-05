package scheduler

import (
	"testing"
	"time"

	"batteryops/internal/domain"
	"batteryops/internal/notify"
	"batteryops/internal/service"
	"batteryops/internal/store"
)

// TestSchedulerAutoTransfer verifies that the scheduler background loop
// detects overdue dispatches and auto-transfers to the backup engineer.
func TestSchedulerAutoTransfer(t *testing.T) {
	st := store.New()
	n := notify.NewSMSNotifier()
	cfg := service.Config{
		ReportSLA:       15 * time.Minute,
		DispatchSLA:     20 * time.Minute,
		TransferTimeout: 50 * time.Millisecond,
	}
	svc := service.New(st, n, cfg)

	st.CreateCabinIfNotExists(domain.NewCabin("cabin-1", "Site A", 45.0))
	result, _ := svc.ReportAlarm("cabin-1", domain.AlarmTempOverLimit, "inspector-1")
	svc.DispatchMaintenance(result.Order.ID, "dispatcher-1", "eng-1", "eng-2")

	sched := New(svc, 20*time.Millisecond)
	sched.Start()
	defer sched.Stop()

	// Wait long enough for at least one tick past the timeout.
	time.Sleep(200 * time.Millisecond)

	order, _ := st.GetMaintenanceOrder(result.Order.ID)
	if order.Status != domain.MaintAssigned {
		t.Fatalf("expected assigned after auto-transfer, got %s", order.Status)
	}
	if order.CurrentEngineerID != "eng-2" {
		t.Fatalf("expected backup engineer eng-2, got %s", order.CurrentEngineerID)
	}
	if order.TransferCount != 1 {
		t.Fatalf("expected transfer count 1, got %d", order.TransferCount)
	}

	msgs := n.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 SMS notifications, got %d", len(msgs))
	}
}

// TestSchedulerInventoryCheck verifies that the scheduler detects parts below
// the safety line and auto-generates replenishment requests.
func TestSchedulerInventoryCheck(t *testing.T) {
	st := store.New()
	n := notify.NewSMSNotifier()
	cfg := service.DefaultConfig()
	svc := service.New(st, n, cfg)

	// Add a part already below the safety line.
	st.CreateSparePartIfNotExists(domain.NewSparePart("part-low", "Contactor", "pcs", 1, 5))

	sched := New(svc, 20*time.Millisecond)
	sched.Start()
	defer sched.Stop()

	time.Sleep(100 * time.Millisecond)

	reqs := st.ListReplenishmentRequests()
	if len(reqs) == 0 {
		t.Fatal("expected at least 1 replenishment request from scheduler")
	}
}

// TestSchedulerStop verifies that the scheduler stops cleanly.
func TestSchedulerStop(t *testing.T) {
	st := store.New()
	n := notify.NewSMSNotifier()
	svc := service.New(st, n, service.DefaultConfig())

	sched := New(svc, 10*time.Millisecond)
	sched.Start()
	time.Sleep(30 * time.Millisecond)
	sched.Stop()

	// After Stop, the scheduler should not be ticking. We verify this by
	// ensuring no panic and the done channel is closed.
	select {
	case _, ok := <-sched.done:
		if ok {
			t.Fatal("expected done channel to be closed")
		}
	default:
		t.Fatal("expected done channel to be closed")
	}
}
