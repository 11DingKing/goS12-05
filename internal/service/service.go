package service

import (
	"fmt"
	"time"

	"batteryops/internal/domain"
	"batteryops/internal/notify"
	"batteryops/internal/store"
)

// Config holds tunable SLA and timeout parameters.
type Config struct {
	ReportSLA       time.Duration
	DispatchSLA     time.Duration
	TransferTimeout time.Duration
}

// DefaultConfig returns production-typical SLA values matching the business
// requirements: 15-minute report window, 20-minute dispatch window, and
// 30-minute auto-transfer timeout.
func DefaultConfig() Config {
	return Config{
		ReportSLA:       15 * time.Minute,
		DispatchSLA:     20 * time.Minute,
		TransferTimeout: 30 * time.Minute,
	}
}

// Service orchestrates all business workflows across the five collaborating
// parties: duty dispatcher, inspection team leader, battery cabin equipment,
// maintenance engineer, and local supply station.
type Service struct {
	store    *store.Store
	notifier notify.Notifier
	cfg      Config
}

// New creates a new Service.
func New(s *store.Store, n notify.Notifier, cfg Config) *Service {
	return &Service{store: s, notifier: n, cfg: cfg}
}

// ReportAlarmResult is the outcome of an alarm report.
type ReportAlarmResult struct {
	Order   *domain.MaintenanceOrder `json:"order"`
	Created bool                     `json:"created"`
	Message string                   `json:"message"`
}

// ---------------------------------------------------------------------------
// Cabin management
// ---------------------------------------------------------------------------

// RegisterCabin adds a new battery cabin.
func (svc *Service) RegisterCabin(id, location string, tempLimit float64) (*domain.Cabin, error) {
	if id == "" {
		return nil, fmt.Errorf("cabin id is required")
	}
	if tempLimit <= 0 {
		return nil, fmt.Errorf("temp_limit must be positive")
	}
	c := domain.NewCabin(id, location, tempLimit)
	if !svc.store.CreateCabinIfNotExists(c) {
		return nil, fmt.Errorf("cabin %s already registered", id)
	}
	return c, nil
}

// ListCabins returns all registered cabins.
func (svc *Service) ListCabins() []*domain.Cabin {
	return svc.store.ListCabins()
}

// SubmitReadings records measurements for a cabin.
func (svc *Service) SubmitReadings(cabinID string, r domain.Readings) (*domain.Cabin, error) {
	return svc.store.UpdateCabin(cabinID, func(c *domain.Cabin) error {
		c.ApplyReadings(r)
		return nil
	})
}

// ---------------------------------------------------------------------------
// Inspection workflow
// ---------------------------------------------------------------------------

// DispatchInspection creates an inspection order per the weekly plan.
func (svc *Service) DispatchInspection(cabinID, inspectorID, planWeek string) (*domain.InspectionOrder, error) {
	if cabinID == "" || inspectorID == "" {
		return nil, fmt.Errorf("cabin_id and inspector_id are required")
	}
	if _, ok := svc.store.GetCabin(cabinID); !ok {
		return nil, fmt.Errorf("cabin %s not found", cabinID)
	}
	id := svc.store.NextID("IO")
	order := domain.NewInspectionOrder(id, cabinID, inspectorID, planWeek)
	svc.store.SaveInspectionOrder(order)
	return order, nil
}

// StartInspection begins an inspection.
func (svc *Service) StartInspection(orderID string) (*domain.InspectionOrder, error) {
	return svc.store.UpdateInspectionOrder(orderID, func(o *domain.InspectionOrder) error {
		return o.Start()
	})
}

// RecordInspectionReadings appends readings during an inspection.
func (svc *Service) RecordInspectionReadings(orderID string, r domain.Readings) (*domain.InspectionOrder, error) {
	return svc.store.UpdateInspectionOrder(orderID, func(o *domain.InspectionOrder) error {
		return o.RecordReadings(r)
	})
}

// CompleteInspection finishes an inspection.
func (svc *Service) CompleteInspection(orderID string) (*domain.InspectionOrder, error) {
	return svc.store.UpdateInspectionOrder(orderID, func(o *domain.InspectionOrder) error {
		return o.Complete()
	})
}

// ListInspectionOrders returns all inspection orders.
func (svc *Service) ListInspectionOrders() []*domain.InspectionOrder {
	return svc.store.ListInspectionOrders()
}

// ---------------------------------------------------------------------------
// Alarm reporting and maintenance dispatch
// ---------------------------------------------------------------------------

// ReportAlarm handles an inspector alarm report.  If an active maintenance
// order already exists for the cabin, the reporter is recorded as a duplicate
// and prompted to confirm rather than creating a second order.
func (svc *Service) ReportAlarm(cabinID string, alarmType domain.AlarmType, reporterID string) (*ReportAlarmResult, error) {
	if reporterID == "" {
		return nil, fmt.Errorf("reporter_id is required")
	}
	if _, ok := svc.store.GetCabin(cabinID); !ok {
		return nil, fmt.Errorf("cabin %s not found", cabinID)
	}
	order, created := svc.store.ReportAlarm(cabinID, alarmType, reporterID)
	if created {
		return &ReportAlarmResult{
			Order:   order,
			Created: true,
			Message: "alarm reported successfully, awaiting dispatcher",
		}, nil
	}
	return &ReportAlarmResult{
		Order:   order,
		Created: false,
		Message: fmt.Sprintf("cabin %s already has active maintenance order %s; please confirm", cabinID, order.ID),
	}, nil
}

// DispatchMaintenance dispatches a maintenance order and locks the cabin
// atomically to prevent duplicate dispatch.
func (svc *Service) DispatchMaintenance(orderID, dispatcherID, engineerID, backupEngineerID string) (*domain.MaintenanceOrder, error) {
	if dispatcherID == "" || engineerID == "" {
		return nil, fmt.Errorf("dispatcher_id and engineer_id are required")
	}
	order, _, err := svc.store.UpdateOrderAndCabin(orderID, func(o *domain.MaintenanceOrder, c *domain.Cabin) error {
		if o.IsDispatchSLABreached(time.Now(), svc.cfg.DispatchSLA) {
			o.AppendAudit("system", "dispatch_sla_breached",
				fmt.Sprintf("reported %v ago without dispatch", time.Since(o.ReportedAt).Round(time.Second)))
		}
		if err := o.Dispatch(dispatcherID, engineerID, backupEngineerID); err != nil {
			return err
		}
		return c.Lock(orderID)
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// EngineerArrive records engineer arrival on site and transitions the cabin
// to under-maintenance.
func (svc *Service) EngineerArrive(orderID, engineerID string) (*domain.MaintenanceOrder, error) {
	order, _, err := svc.store.UpdateOrderAndCabin(orderID, func(o *domain.MaintenanceOrder, c *domain.Cabin) error {
		if err := o.EngineerArrive(engineerID); err != nil {
			return err
		}
		return c.StartMaintenance()
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// CompleteProcessing marks the engineer's work as done.
func (svc *Service) CompleteProcessing(orderID, engineerID string) (*domain.MaintenanceOrder, error) {
	return svc.store.UpdateMaintenanceOrder(orderID, func(o *domain.MaintenanceOrder) error {
		return o.CompleteProcessing(engineerID)
	})
}

// AcceptMaintenance performs dual acceptance by the inspection team leader and
// the supply-station attendant, then closes the order and unlocks the cabin.
func (svc *Service) AcceptMaintenance(orderID, leaderID, stationID string) (*domain.MaintenanceOrder, error) {
	if leaderID == "" || stationID == "" {
		return nil, fmt.Errorf("dual acceptance requires both leader_id and station_id")
	}
	order, _, err := svc.store.UpdateOrderAndCabin(orderID, func(o *domain.MaintenanceOrder, c *domain.Cabin) error {
		if err := o.Accept(leaderID, stationID); err != nil {
			return err
		}
		c.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// TransferOrder transfers a maintenance order to its backup engineer and sends
// SMS notifications to both engineers.
func (svc *Service) TransferOrder(orderID string) (*domain.MaintenanceOrder, error) {
	order, err := svc.store.UpdateMaintenanceOrder(orderID, func(o *domain.MaintenanceOrder) error {
		return o.Transfer()
	})
	if err != nil {
		return nil, err
	}
	svc.notifier.Send(order.PrimaryEngineerID,
		fmt.Sprintf("Maintenance order %s for cabin %s has been transferred to backup engineer %s",
			order.ID, order.CabinID, order.BackupEngineerID))
	svc.notifier.Send(order.BackupEngineerID,
		fmt.Sprintf("Maintenance order %s for cabin %s has been transferred to you. Please proceed to site.",
			order.ID, order.CabinID))
	return order, nil
}

// CheckOverdueTransfers finds dispatched orders whose engineer has not arrived
// within the transfer timeout and transfers them to the backup engineer.
func (svc *Service) CheckOverdueTransfers() []*domain.MaintenanceOrder {
	overdue := svc.store.OverdueTransferOrders(svc.cfg.TransferTimeout)
	var transferred []*domain.MaintenanceOrder
	for _, o := range overdue {
		if t, err := svc.TransferOrder(o.ID); err == nil {
			transferred = append(transferred, t)
		}
	}
	return transferred
}

// CheckOverdueDispatches returns reported orders that have breached the
// dispatch SLA.
func (svc *Service) CheckOverdueDispatches() []*domain.MaintenanceOrder {
	return svc.store.OverdueDispatchOrders(svc.cfg.DispatchSLA)
}

// ListMaintenanceOrders returns all maintenance orders.
func (svc *Service) ListMaintenanceOrders() []*domain.MaintenanceOrder {
	return svc.store.ListMaintenanceOrders()
}

// ---------------------------------------------------------------------------
// Inventory management
// ---------------------------------------------------------------------------

// AddSparePart registers a new spare part.
func (svc *Service) AddSparePart(id, name, unit string, stock, safetyLine int) (*domain.SparePart, error) {
	if id == "" {
		return nil, fmt.Errorf("part id is required")
	}
	if safetyLine < 0 || stock < 0 {
		return nil, fmt.Errorf("stock and safety_line must be non-negative")
	}
	p := domain.NewSparePart(id, name, unit, stock, safetyLine)
	if !svc.store.CreateSparePartIfNotExists(p) {
		return nil, fmt.Errorf("spare part %s already exists", id)
	}
	return p, nil
}

// ConsumePart consumes stock and auto-generates a replenishment request when
// the stock drops below the safety line.
func (svc *Service) ConsumePart(partID string, qty int) (*domain.SparePart, *domain.ReplenishmentRequest, error) {
	part, req, err := svc.store.ConsumePart(partID, qty)
	if err != nil {
		return nil, nil, err
	}
	if req != nil {
		svc.notifier.Send("inventory",
			fmt.Sprintf("Replenishment request %s created for part %s (qty %d)",
				req.ID, req.PartID, req.Quantity))
	}
	return part, req, nil
}

// CheckInventory scans all parts and ensures replenishment requests exist for
// any part below its safety line.
func (svc *Service) CheckInventory() []*domain.ReplenishmentRequest {
	parts := svc.store.CheckInventoryBelowSafety()
	var reqs []*domain.ReplenishmentRequest
	for _, p := range parts {
		if req := svc.store.EnsureReplenishmentForPart(p.ID); req != nil {
			reqs = append(reqs, req)
			svc.notifier.Send("inventory",
				fmt.Sprintf("Replenishment request %s created for part %s (qty %d)",
					req.ID, req.PartID, req.Quantity))
		}
	}
	return reqs
}

// ListSpareParts returns all spare parts.
func (svc *Service) ListSpareParts() []*domain.SparePart {
	return svc.store.ListSpareParts()
}

// ListReplenishmentRequests returns all replenishment requests.
func (svc *Service) ListReplenishmentRequests() []*domain.ReplenishmentRequest {
	return svc.store.ListReplenishmentRequests()
}

// ---------------------------------------------------------------------------
// Operations (grid switch / black start)
// ---------------------------------------------------------------------------

// InitiateOperation starts a dual-confirmation operation initiated by the
// duty dispatcher.
func (svc *Service) InitiateOperation(opType domain.OperationType, dispatcherID string) (*domain.Operation, error) {
	if dispatcherID == "" {
		return nil, fmt.Errorf("dispatcher_id is required")
	}
	id := svc.store.NextID("OP")
	op := domain.NewOperation(id, opType, dispatcherID)
	svc.store.SaveOperation(op)
	return op, nil
}

// ConfirmOperation records supply-station confirmation as the second leg of
// the dual confirmation.
func (svc *Service) ConfirmOperation(opID, stationID string) (*domain.Operation, error) {
	if stationID == "" {
		return nil, fmt.Errorf("station_id is required")
	}
	return svc.store.UpdateOperation(opID, func(op *domain.Operation) error {
		return op.ConfirmByStation(stationID)
	})
}

// ExecuteOperation executes a dual-confirmed operation.
func (svc *Service) ExecuteOperation(opID string) (*domain.Operation, error) {
	return svc.store.UpdateOperation(opID, func(op *domain.Operation) error {
		return op.Execute()
	})
}

// CancelOperation cancels a pending or confirmed operation.
func (svc *Service) CancelOperation(opID, actor string) (*domain.Operation, error) {
	return svc.store.UpdateOperation(opID, func(op *domain.Operation) error {
		return op.Cancel(actor)
	})
}

// ListOperations returns all operations.
func (svc *Service) ListOperations() []*domain.Operation {
	return svc.store.ListOperations()
}
