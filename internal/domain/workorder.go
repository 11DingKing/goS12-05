package domain

import (
	"fmt"
	"time"
)

// AlarmType classifies the kind of anomaly reported by an inspector.
type AlarmType string

const (
	AlarmTempOverLimit AlarmType = "temperature_over_limit"
	AlarmInsulation    AlarmType = "insulation"
)

// MaintenanceStatus is the lifecycle state of a maintenance work order.
type MaintenanceStatus string

const (
	MaintReported          MaintenanceStatus = "reported"
	MaintDispatched        MaintenanceStatus = "dispatched"
	MaintAssigned          MaintenanceStatus = "assigned"
	MaintProcessing        MaintenanceStatus = "processing"
	MaintPendingAcceptance MaintenanceStatus = "pending_acceptance"
	MaintClosed            MaintenanceStatus = "closed"
)

// AuditEntry records a single traceable operation for full audit trail.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
}

// MaintenanceOrder orchestrates the repair workflow for an alarmed cabin.
type MaintenanceOrder struct {
	ID                   string            `json:"id"`
	CabinID              string            `json:"cabin_id"`
	AlarmType            AlarmType         `json:"alarm_type"`
	ReporterID           string            `json:"reporter_id"`
	Status               MaintenanceStatus `json:"status"`
	DispatcherID         string            `json:"dispatcher_id,omitempty"`
	PrimaryEngineerID    string            `json:"primary_engineer_id,omitempty"`
	BackupEngineerID     string            `json:"backup_engineer_id,omitempty"`
	CurrentEngineerID    string            `json:"current_engineer_id,omitempty"`
	DispatchedAt         *time.Time        `json:"dispatched_at,omitempty"`
	ArrivedAt            *time.Time        `json:"arrived_at,omitempty"`
	AcceptedByLeader     string            `json:"accepted_by_leader,omitempty"`
	AcceptedByStation    string            `json:"accepted_by_station,omitempty"`
	ReportedAt           time.Time         `json:"reported_at"`
	ClosedAt             *time.Time        `json:"closed_at,omitempty"`
	TransferCount        int               `json:"transfer_count"`
	DuplicateReporterIDs []string          `json:"duplicate_reporter_ids,omitempty"`
	AuditTrail           []AuditEntry      `json:"audit_trail"`
}

// NewMaintenanceOrder creates a reported maintenance order.
func NewMaintenanceOrder(id, cabinID string, alarmType AlarmType, reporterID string) *MaintenanceOrder {
	now := time.Now()
	return &MaintenanceOrder{
		ID:         id,
		CabinID:    cabinID,
		AlarmType:  alarmType,
		ReporterID: reporterID,
		Status:     MaintReported,
		ReportedAt: now,
		AuditTrail: []AuditEntry{
			{Timestamp: now, Actor: reporterID, Action: "alarm_reported", Detail: string(alarmType)},
		},
	}
}

// IsActive reports whether the order is still open.
func (o *MaintenanceOrder) IsActive() bool {
	return o.Status != MaintClosed
}

// AppendAudit adds a traceable entry to the audit trail.
func (o *MaintenanceOrder) AppendAudit(actor, action, detail string) {
	o.AuditTrail = append(o.AuditTrail, AuditEntry{
		Timestamp: time.Now(),
		Actor:     actor,
		Action:    action,
		Detail:    detail,
	})
}

// Dispatch transitions from reported to dispatched and records the assignment.
func (o *MaintenanceOrder) Dispatch(dispatcherID, engineerID, backupEngineerID string) error {
	if o.Status != MaintReported {
		return fmt.Errorf("cannot dispatch order %s in status %s", o.ID, o.Status)
	}
	now := time.Now()
	o.DispatcherID = dispatcherID
	o.PrimaryEngineerID = engineerID
	o.BackupEngineerID = backupEngineerID
	o.CurrentEngineerID = engineerID
	o.Status = MaintDispatched
	o.DispatchedAt = &now
	o.AppendAudit(dispatcherID, "dispatched",
		fmt.Sprintf("engineer=%s backup=%s", engineerID, backupEngineerID))
	return nil
}

// AddDuplicateReporter records a subsequent reporter without creating a new order.
func (o *MaintenanceOrder) AddDuplicateReporter(reporterID string) {
	o.DuplicateReporterIDs = append(o.DuplicateReporterIDs, reporterID)
	o.AppendAudit(reporterID, "duplicate_report_suppressed",
		"already has active order for this cabin")
}

// EngineerArrive transitions from dispatched or assigned to processing.
func (o *MaintenanceOrder) EngineerArrive(engineerID string) error {
	if o.Status != MaintDispatched && o.Status != MaintAssigned {
		return fmt.Errorf("cannot mark arrival for order %s in status %s", o.ID, o.Status)
	}
	if o.CurrentEngineerID != engineerID {
		return fmt.Errorf("engineer %s is not the assigned engineer for order %s", engineerID, o.ID)
	}
	now := time.Now()
	o.Status = MaintProcessing
	o.ArrivedAt = &now
	o.AppendAudit(engineerID, "engineer_arrived", "")
	return nil
}

// CompleteProcessing transitions from processing to pending acceptance.
func (o *MaintenanceOrder) CompleteProcessing(engineerID string) error {
	if o.Status != MaintProcessing {
		return fmt.Errorf("cannot complete order %s in status %s", o.ID, o.Status)
	}
	if o.CurrentEngineerID != engineerID {
		return fmt.Errorf("engineer %s is not the assigned engineer for order %s", engineerID, o.ID)
	}
	o.Status = MaintPendingAcceptance
	o.AppendAudit(engineerID, "processing_completed", "")
	return nil
}

// Accept performs dual acceptance and closes the order.
func (o *MaintenanceOrder) Accept(leaderID, stationID string) error {
	if o.Status != MaintPendingAcceptance {
		return fmt.Errorf("cannot accept order %s in status %s", o.ID, o.Status)
	}
	if leaderID == "" || stationID == "" {
		return fmt.Errorf("dual acceptance requires both leader and station identifiers")
	}
	now := time.Now()
	o.AcceptedByLeader = leaderID
	o.AcceptedByStation = stationID
	o.Status = MaintClosed
	o.ClosedAt = &now
	o.AppendAudit(leaderID, "accepted_by_leader", "")
	o.AppendAudit(stationID, "accepted_by_station", "")
	o.AppendAudit("system", "order_closed", "")
	return nil
}

// Transfer reassigns the order to the backup engineer.
func (o *MaintenanceOrder) Transfer() error {
	if o.BackupEngineerID == "" {
		return fmt.Errorf("order %s has no backup engineer", o.ID)
	}
	if o.Status != MaintDispatched {
		return fmt.Errorf("cannot transfer order %s in status %s", o.ID, o.Status)
	}
	o.CurrentEngineerID = o.BackupEngineerID
	o.Status = MaintAssigned
	o.TransferCount++
	o.AppendAudit("system", "auto_transferred",
		fmt.Sprintf("from=%s to=%s", o.PrimaryEngineerID, o.BackupEngineerID))
	return nil
}

// IsTransferOverdue returns true when the dispatch has exceeded the timeout
// without the primary engineer arriving.
func (o *MaintenanceOrder) IsTransferOverdue(now time.Time, timeout time.Duration) bool {
	if o.Status != MaintDispatched {
		return false
	}
	if o.DispatchedAt == nil {
		return false
	}
	return now.Sub(*o.DispatchedAt) > timeout
}

// IsDispatchSLABreached returns true when the dispatcher has exceeded the
// dispatch SLA since the alarm was reported.
func (o *MaintenanceOrder) IsDispatchSLABreached(now time.Time, sla time.Duration) bool {
	if o.Status != MaintReported {
		return false
	}
	return now.Sub(o.ReportedAt) > sla
}

// InspectionStatus is the lifecycle state of an inspection work order.
type InspectionStatus string

const (
	InspectionCreated    InspectionStatus = "created"
	InspectionInProgress InspectionStatus = "in_progress"
	InspectionCompleted  InspectionStatus = "completed"
)

// InspectionOrder represents a weekly inspection task assigned to an inspector.
type InspectionOrder struct {
	ID          string           `json:"id"`
	CabinID     string           `json:"cabin_id"`
	InspectorID string           `json:"inspector_id"`
	Status      InspectionStatus `json:"status"`
	PlanWeek    string           `json:"plan_week"`
	CreatedAt   time.Time        `json:"created_at"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	Readings    []Readings       `json:"readings,omitempty"`
	AuditTrail  []AuditEntry     `json:"audit_trail"`
}

// NewInspectionOrder creates a new inspection order.
func NewInspectionOrder(id, cabinID, inspectorID, planWeek string) *InspectionOrder {
	now := time.Now()
	return &InspectionOrder{
		ID:          id,
		CabinID:     cabinID,
		InspectorID: inspectorID,
		Status:      InspectionCreated,
		PlanWeek:    planWeek,
		CreatedAt:   now,
		AuditTrail: []AuditEntry{
			{Timestamp: now, Actor: inspectorID, Action: "inspection_dispatched", Detail: planWeek},
		},
	}
}

// Start transitions the inspection to in-progress.
func (o *InspectionOrder) Start() error {
	if o.Status != InspectionCreated {
		return fmt.Errorf("cannot start inspection %s in status %s", o.ID, o.Status)
	}
	o.Status = InspectionInProgress
	o.AuditTrail = append(o.AuditTrail, AuditEntry{
		Timestamp: time.Now(),
		Actor:     o.InspectorID,
		Action:    "inspection_started",
	})
	return nil
}

// RecordReadings appends readings to the inspection.
func (o *InspectionOrder) RecordReadings(r Readings) error {
	if o.Status != InspectionInProgress && o.Status != InspectionCreated {
		return fmt.Errorf("cannot record readings for inspection %s in status %s", o.ID, o.Status)
	}
	o.Readings = append(o.Readings, r)
	return nil
}

// Complete finishes the inspection.
func (o *InspectionOrder) Complete() error {
	if o.Status == InspectionCompleted {
		return nil
	}
	if o.Status != InspectionInProgress && o.Status != InspectionCreated {
		return fmt.Errorf("cannot complete inspection %s in status %s", o.ID, o.Status)
	}
	now := time.Now()
	o.Status = InspectionCompleted
	o.CompletedAt = &now
	o.AuditTrail = append(o.AuditTrail, AuditEntry{
		Timestamp: now,
		Actor:     o.InspectorID,
		Action:    "inspection_completed",
	})
	return nil
}
