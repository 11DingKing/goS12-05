package store

import (
	"sync"
	"testing"

	"batteryops/internal/domain"
)

// TestStoreReportAlarmIdempotent verifies that reporting an alarm twice for
// the same cabin creates only one order and records the second reporter as a
// duplicate.
func TestStoreReportAlarmIdempotent(t *testing.T) {
	s := New()
	s.CreateCabinIfNotExists(domain.NewCabin("cabin-1", "Site A", 45.0))

	order1, created1 := s.ReportAlarm("cabin-1", domain.AlarmTempOverLimit, "inspector-A")
	if !created1 {
		t.Fatal("expected first alarm to create a new order")
	}

	order2, created2 := s.ReportAlarm("cabin-1", domain.AlarmInsulation, "inspector-B")
	if created2 {
		t.Fatal("expected second alarm to NOT create a new order")
	}
	if order1.ID != order2.ID {
		t.Fatal("expected same order for duplicate alarm")
	}
	if len(order2.DuplicateReporterIDs) != 1 {
		t.Fatalf("expected 1 duplicate reporter, got %d", len(order2.DuplicateReporterIDs))
	}
	if order2.DuplicateReporterIDs[0] != "inspector-B" {
		t.Fatalf("expected duplicate reporter inspector-B, got %s", order2.DuplicateReporterIDs[0])
	}
}

// TestStoreConcurrentReportAlarm verifies that under concurrent alarm reports
// for the same cabin, only one order is created.
func TestStoreConcurrentReportAlarm(t *testing.T) {
	s := New()
	s.CreateCabinIfNotExists(domain.NewCabin("cabin-2", "Site B", 45.0))

	var wg sync.WaitGroup
	reporters := []string{"inspector-1", "inspector-2", "inspector-3", "inspector-4", "inspector-5"}
	created := make([]bool, len(reporters))

	for i, reporter := range reporters {
		wg.Add(1)
		go func(idx int, r string) {
			defer wg.Done()
			_, c := s.ReportAlarm("cabin-2", domain.AlarmTempOverLimit, r)
			created[idx] = c
		}(i, reporter)
	}
	wg.Wait()

	createdCount := 0
	for _, c := range created {
		if c {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("expected exactly 1 order created, got %d", createdCount)
	}

	orders := s.ListMaintenanceOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 maintenance order, got %d", len(orders))
	}
	// 4 duplicate reporters + 1 original = 5 total
	if len(orders[0].DuplicateReporterIDs) != 4 {
		t.Fatalf("expected 4 duplicate reporters, got %d", len(orders[0].DuplicateReporterIDs))
	}
}

// TestStoreCabinLockUnlock verifies cabin locking is idempotent and prevents
// double-lock by different orders.
func TestStoreCabinLockUnlock(t *testing.T) {
	s := New()
	c := domain.NewCabin("cabin-3", "Site C", 45.0)
	s.CreateCabinIfNotExists(c)

	if err := c.Lock("MO-1"); err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if c.Status != domain.CabinLocked {
		t.Fatalf("expected locked, got %s", c.Status)
	}

	// Idempotent: lock again with same order.
	if err := c.Lock("MO-1"); err != nil {
		t.Fatalf("idempotent lock: %v", err)
	}

	// Different order cannot lock.
	if err := c.Lock("MO-2"); err == nil {
		t.Fatal("expected error locking with different order")
	}

	c.Unlock()
	if c.Status != domain.CabinNormal {
		t.Fatalf("expected normal after unlock, got %s", c.Status)
	}
}

// TestStoreConsumePartReplenishment verifies that consuming stock below the
// safety line auto-generates a replenishment request, and that a second
// consumption does not create a duplicate request.
func TestStoreConsumePartReplenishment(t *testing.T) {
	s := New()
	p := domain.NewSparePart("part-1", "Fuse", "pcs", 5, 3)
	s.CreateSparePartIfNotExists(p)

	// Consume 1: stock goes to 4, still above safety line (3). No replenishment.
	_, req, err := s.ConsumePart("part-1", 1)
	if err != nil {
		t.Fatalf("consume 1: %v", err)
	}
	if req != nil {
		t.Fatal("did not expect replenishment when stock is still above safety line")
	}

	// Consume 2: stock goes to 2, below safety line (3). Replenishment expected.
	_, req, err = s.ConsumePart("part-1", 2)
	if err != nil {
		t.Fatalf("consume 2: %v", err)
	}
	if req == nil {
		t.Fatal("expected replenishment request")
	}

	// Consume 1 more: stock goes to 1, still below safety, but no new request.
	_, req2, err := s.ConsumePart("part-1", 1)
	if err != nil {
		t.Fatalf("consume 3: %v", err)
	}
	if req2 != nil {
		t.Fatal("did not expect duplicate replenishment request")
	}

	reqs := s.ListReplenishmentRequests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 replenishment request, got %d", len(reqs))
	}
}

// TestStoreEnsureReplenishmentForPart verifies that the scheduler-style
// inventory check creates a request when a part is below safety line.
func TestStoreEnsureReplenishmentForPart(t *testing.T) {
	s := New()
	p := domain.NewSparePart("part-2", "Contactor", "pcs", 2, 5)
	s.CreateSparePartIfNotExists(p)

	// Below safety line → should create request.
	req := s.EnsureReplenishmentForPart("part-2")
	if req == nil {
		t.Fatal("expected replenishment request")
	}

	// Calling again should be idempotent (no new request).
	req2 := s.EnsureReplenishmentForPart("part-2")
	if req2 != nil {
		t.Fatal("expected no duplicate replenishment request")
	}

	// Part above safety line → no request.
	p2 := domain.NewSparePart("part-3", "Relay", "pcs", 10, 3)
	s.CreateSparePartIfNotExists(p2)
	if r := s.EnsureReplenishmentForPart("part-3"); r != nil {
		t.Fatal("did not expect replenishment for part above safety line")
	}
}

// TestStoreUpdateOrderAndCabin verifies the atomic cross-entity update.
func TestStoreUpdateOrderAndCabin(t *testing.T) {
	s := New()
	s.CreateCabinIfNotExists(domain.NewCabin("cabin-10", "Site X", 45.0))
	order, _ := s.ReportAlarm("cabin-10", domain.AlarmTempOverLimit, "inspector-1")

	// Atomically dispatch and lock cabin.
	o, c, err := s.UpdateOrderAndCabin(order.ID, func(ord *domain.MaintenanceOrder, cab *domain.Cabin) error {
		if err := ord.Dispatch("dispatcher-1", "eng-1", "eng-2"); err != nil {
			return err
		}
		return cab.Lock(order.ID)
	})
	if err != nil {
		t.Fatalf("update order and cabin: %v", err)
	}
	if o.Status != domain.MaintDispatched {
		t.Fatalf("expected dispatched, got %s", o.Status)
	}
	if c.Status != domain.CabinLocked {
		t.Fatalf("expected locked, got %s", c.Status)
	}

	// Non-existent order returns error.
	_, _, err = s.UpdateOrderAndCabin("nonexistent", func(ord *domain.MaintenanceOrder, cab *domain.Cabin) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
}
