package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"batteryops/internal/domain"
)

// Store is a concurrency-safe in-memory persistence layer for all domain
// aggregates.  All mutating operations that span multiple entities are
// performed atomically under a single lock to preserve invariants.
type Store struct {
	mu                sync.RWMutex
	cabins            map[string]*domain.Cabin
	maintenanceOrders map[string]*domain.MaintenanceOrder
	inspectionOrders  map[string]*domain.InspectionOrder
	parts             map[string]*domain.SparePart
	replenishments    map[string]*domain.ReplenishmentRequest
	operations        map[string]*domain.Operation
	idCounter         uint64
}

// New creates an empty Store.
func New() *Store {
	return &Store{
		cabins:            make(map[string]*domain.Cabin),
		maintenanceOrders: make(map[string]*domain.MaintenanceOrder),
		inspectionOrders:  make(map[string]*domain.InspectionOrder),
		parts:             make(map[string]*domain.SparePart),
		replenishments:    make(map[string]*domain.ReplenishmentRequest),
		operations:        make(map[string]*domain.Operation),
	}
}

// NextID generates a unique identifier with the given prefix.
func (s *Store) NextID(prefix string) string {
	n := atomic.AddUint64(&s.idCounter, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), n)
}

// ---------------------------------------------------------------------------
// Cabins
// ---------------------------------------------------------------------------

// CreateCabinIfNotExists atomically inserts a cabin.  Returns false if a cabin
// with the same ID already exists.
func (s *Store) CreateCabinIfNotExists(c *domain.Cabin) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cabins[c.ID]; exists {
		return false
	}
	s.cabins[c.ID] = c
	return true
}

// GetCabin returns a cabin by ID.
func (s *Store) GetCabin(id string) (*domain.Cabin, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.cabins[id]
	return c, ok
}

// ListCabins returns all registered cabins.
func (s *Store) ListCabins() []*domain.Cabin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.Cabin, 0, len(s.cabins))
	for _, c := range s.cabins {
		result = append(result, c)
	}
	return result
}

// UpdateCabin atomically reads, mutates, and persists a cabin.
func (s *Store) UpdateCabin(id string, fn func(*domain.Cabin) error) (*domain.Cabin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cabins[id]
	if !ok {
		return nil, fmt.Errorf("cabin %s not found", id)
	}
	if err := fn(c); err != nil {
		return nil, err
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Maintenance orders
// ---------------------------------------------------------------------------

// ReportAlarm atomically checks for an existing active maintenance order for
// the given cabin.  If one exists, the reporter is recorded as a duplicate and
// the existing order is returned with created=false.  Otherwise a new order is
// created and returned with created=true.  This prevents duplicate orders even
// under concurrent alarm reports.
func (s *Store) ReportAlarm(cabinID string, alarmType domain.AlarmType, reporterID string) (*domain.MaintenanceOrder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.maintenanceOrders {
		if o.CabinID == cabinID && o.IsActive() {
			o.AddDuplicateReporter(reporterID)
			return o, false
		}
	}
	order := domain.NewMaintenanceOrder(s.NextID("MO"), cabinID, alarmType, reporterID)
	s.maintenanceOrders[order.ID] = order
	return order, true
}

// GetMaintenanceOrder returns a maintenance order by ID.
func (s *Store) GetMaintenanceOrder(id string) (*domain.MaintenanceOrder, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.maintenanceOrders[id]
	return o, ok
}

// ListMaintenanceOrders returns all maintenance orders.
func (s *Store) ListMaintenanceOrders() []*domain.MaintenanceOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.MaintenanceOrder, 0, len(s.maintenanceOrders))
	for _, o := range s.maintenanceOrders {
		result = append(result, o)
	}
	return result
}

// UpdateMaintenanceOrder atomically reads, mutates, and persists a maintenance
// order.
func (s *Store) UpdateMaintenanceOrder(id string, fn func(*domain.MaintenanceOrder) error) (*domain.MaintenanceOrder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.maintenanceOrders[id]
	if !ok {
		return nil, fmt.Errorf("maintenance order %s not found", id)
	}
	if err := fn(o); err != nil {
		return nil, err
	}
	return o, nil
}

// UpdateOrderAndCabin atomically updates a maintenance order and its
// associated cabin.  This is used for dispatch, arrival, and acceptance to
// keep the order state and cabin lock consistent under a single lock.
func (s *Store) UpdateOrderAndCabin(orderID string, fn func(o *domain.MaintenanceOrder, c *domain.Cabin) error) (*domain.MaintenanceOrder, *domain.Cabin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.maintenanceOrders[orderID]
	if !ok {
		return nil, nil, fmt.Errorf("maintenance order %s not found", orderID)
	}
	c, ok := s.cabins[o.CabinID]
	if !ok {
		return nil, nil, fmt.Errorf("cabin %s not found", o.CabinID)
	}
	if err := fn(o, c); err != nil {
		return nil, nil, err
	}
	return o, c, nil
}

// OverdueTransferOrders returns dispatched orders that have exceeded the
// transfer timeout without the engineer arriving.
func (s *Store) OverdueTransferOrders(timeout time.Duration) []*domain.MaintenanceOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	result := make([]*domain.MaintenanceOrder, 0)
	for _, o := range s.maintenanceOrders {
		if o.IsTransferOverdue(now, timeout) {
			result = append(result, o)
		}
	}
	return result
}

// OverdueDispatchOrders returns reported orders that have breached the
// dispatch SLA.
func (s *Store) OverdueDispatchOrders(sla time.Duration) []*domain.MaintenanceOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	result := make([]*domain.MaintenanceOrder, 0)
	for _, o := range s.maintenanceOrders {
		if o.IsDispatchSLABreached(now, sla) {
			result = append(result, o)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Inspection orders
// ---------------------------------------------------------------------------

// SaveInspectionOrder persists a new or updated inspection order.
func (s *Store) SaveInspectionOrder(o *domain.InspectionOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inspectionOrders[o.ID] = o
}

// GetInspectionOrder returns an inspection order by ID.
func (s *Store) GetInspectionOrder(id string) (*domain.InspectionOrder, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.inspectionOrders[id]
	return o, ok
}

// ListInspectionOrders returns all inspection orders.
func (s *Store) ListInspectionOrders() []*domain.InspectionOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.InspectionOrder, 0, len(s.inspectionOrders))
	for _, o := range s.inspectionOrders {
		result = append(result, o)
	}
	return result
}

// UpdateInspectionOrder atomically reads, mutates, and persists an inspection
// order.
func (s *Store) UpdateInspectionOrder(id string, fn func(*domain.InspectionOrder) error) (*domain.InspectionOrder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.inspectionOrders[id]
	if !ok {
		return nil, fmt.Errorf("inspection order %s not found", id)
	}
	if err := fn(o); err != nil {
		return nil, err
	}
	return o, nil
}

// ---------------------------------------------------------------------------
// Spare parts and replenishment
// ---------------------------------------------------------------------------

// CreateSparePartIfNotExists atomically inserts a spare part.
func (s *Store) CreateSparePartIfNotExists(p *domain.SparePart) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.parts[p.ID]; exists {
		return false
	}
	s.parts[p.ID] = p
	return true
}

// GetSparePart returns a spare part by ID.
func (s *Store) GetSparePart(id string) (*domain.SparePart, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.parts[id]
	return p, ok
}

// ListSpareParts returns all spare parts.
func (s *Store) ListSpareParts() []*domain.SparePart {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.SparePart, 0, len(s.parts))
	for _, p := range s.parts {
		result = append(result, p)
	}
	return result
}

// ConsumePart atomically consumes stock and creates a replenishment request
// if the stock drops below the safety line and no pending request exists.
func (s *Store) ConsumePart(partID string, qty int) (*domain.SparePart, *domain.ReplenishmentRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.parts[partID]
	if !ok {
		return nil, nil, fmt.Errorf("consume part %s: %w", partID, domain.ErrPartNotFound)
	}
	if err := p.Consume(qty); err != nil {
		return nil, nil, fmt.Errorf("consume part %s rejected: %s", partID, err.Error())
	}
	var req *domain.ReplenishmentRequest
	if p.BelowSafetyLine() {
		exists := false
		for _, r := range s.replenishments {
			if r.PartID == partID && r.Status == domain.ReplenishmentPending {
				exists = true
				break
			}
		}
		if !exists {
			req = domain.NewReplenishmentRequest(
				s.NextID("RR"),
				partID,
				p.SafetyLine-p.Stock+10,
			)
			s.replenishments[req.ID] = req
		}
	}
	return p, req, nil
}

// CheckInventoryBelowSafety returns parts currently below their safety line.
func (s *Store) CheckInventoryBelowSafety() []*domain.SparePart {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.SparePart, 0)
	for _, p := range s.parts {
		if p.BelowSafetyLine() {
			result = append(result, p)
		}
	}
	return result
}

// EnsureReplenishmentForPart creates a replenishment request if the part is
// below its safety line and no pending request exists.  Returns nil if not
// needed.
func (s *Store) EnsureReplenishmentForPart(partID string) *domain.ReplenishmentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.parts[partID]
	if !ok || !p.BelowSafetyLine() {
		return nil
	}
	for _, r := range s.replenishments {
		if r.PartID == partID && r.Status == domain.ReplenishmentPending {
			return nil
		}
	}
	req := domain.NewReplenishmentRequest(
		s.NextID("RR"),
		partID,
		p.SafetyLine-p.Stock+10,
	)
	s.replenishments[req.ID] = req
	return req
}

// ListReplenishmentRequests returns all replenishment requests.
func (s *Store) ListReplenishmentRequests() []*domain.ReplenishmentRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.ReplenishmentRequest, 0, len(s.replenishments))
	for _, r := range s.replenishments {
		result = append(result, r)
	}
	return result
}

// ---------------------------------------------------------------------------
// Operations (grid switch / black start)
// ---------------------------------------------------------------------------

// SaveOperation persists a new or updated operation.
func (s *Store) SaveOperation(op *domain.Operation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations[op.ID] = op
}

// GetOperation returns an operation by ID.
func (s *Store) GetOperation(id string) (*domain.Operation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	op, ok := s.operations[id]
	return op, ok
}

// ListOperations returns all operations.
func (s *Store) ListOperations() []*domain.Operation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*domain.Operation, 0, len(s.operations))
	for _, op := range s.operations {
		result = append(result, op)
	}
	return result
}

// UpdateOperation atomically reads, mutates, and persists an operation.
func (s *Store) UpdateOperation(id string, fn func(*domain.Operation) error) (*domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[id]
	if !ok {
		return nil, fmt.Errorf("operation %s not found", id)
	}
	if err := fn(op); err != nil {
		return nil, err
	}
	return op, nil
}
