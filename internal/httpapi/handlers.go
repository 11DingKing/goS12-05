package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"batteryops/internal/domain"
	"batteryops/internal/service"
)

// Server wires the service into HTTP handlers.
type Server struct {
	svc *service.Service
	mux *http.ServeMux
}

// New creates a new HTTP Server.
func NewServer(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

// Handler returns the underlying http.Handler for use with http.Server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)

	s.mux.HandleFunc("POST /api/cabins", s.handleRegisterCabin)
	s.mux.HandleFunc("GET /api/cabins", s.handleListCabins)
	s.mux.HandleFunc("POST /api/cabins/{id}/readings", s.handleSubmitReadings)
	s.mux.HandleFunc("POST /api/cabins/{id}/alarm", s.handleReportAlarm)

	s.mux.HandleFunc("POST /api/workorders/inspection", s.handleDispatchInspection)
	s.mux.HandleFunc("POST /api/workorders/inspection/{id}/start", s.handleStartInspection)
	s.mux.HandleFunc("POST /api/workorders/inspection/{id}/readings", s.handleRecordReadings)
	s.mux.HandleFunc("POST /api/workorders/inspection/{id}/complete", s.handleCompleteInspection)

	s.mux.HandleFunc("POST /api/workorders/maintenance/{id}/dispatch", s.handleDispatchMaintenance)
	s.mux.HandleFunc("POST /api/workorders/maintenance/{id}/arrive", s.handleEngineerArrive)
	s.mux.HandleFunc("POST /api/workorders/maintenance/{id}/complete", s.handleCompleteProcessing)
	s.mux.HandleFunc("POST /api/workorders/maintenance/{id}/accept", s.handleAcceptMaintenance)
	s.mux.HandleFunc("POST /api/workorders/maintenance/{id}/transfer", s.handleTransferOrder)
	s.mux.HandleFunc("GET /api/workorders", s.handleListWorkOrders)

	s.mux.HandleFunc("POST /api/inventory/parts", s.handleAddPart)
	s.mux.HandleFunc("GET /api/inventory/parts", s.handleListParts)
	s.mux.HandleFunc("POST /api/inventory/parts/{id}/consume", s.handleConsumePart)
	s.mux.HandleFunc("GET /api/inventory/replenishments", s.handleListReplenishments)

	s.mux.HandleFunc("POST /api/operations", s.handleInitiateOperation)
	s.mux.HandleFunc("POST /api/operations/{id}/confirm", s.handleConfirmOperation)
	s.mux.HandleFunc("POST /api/operations/{id}/execute", s.handleExecuteOperation)
	s.mux.HandleFunc("POST /api/operations/{id}/cancel", s.handleCancelOperation)
	s.mux.HandleFunc("GET /api/operations", s.handleListOperations)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Cabin endpoints
// ---------------------------------------------------------------------------

type registerCabinReq struct {
	ID        string  `json:"id"`
	Location  string  `json:"location"`
	TempLimit float64 `json:"temp_limit"`
}

func (s *Server) handleRegisterCabin(w http.ResponseWriter, r *http.Request) {
	var req registerCabinReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cabin, err := s.svc.RegisterCabin(req.ID, req.Location, req.TempLimit)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cabin)
}

func (s *Server) handleListCabins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.ListCabins())
}

type readingsReq struct {
	Temperature  float64 `json:"temperature"`
	Humidity     float64 `json:"humidity"`
	Voltage      float64 `json:"voltage"`
	InsulationOK bool    `json:"insulation_ok"`
}

func (s *Server) handleSubmitReadings(w http.ResponseWriter, r *http.Request) {
	cabinID := r.PathValue("id")
	var req readingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cabin, err := s.svc.SubmitReadings(cabinID, domain.Readings{
		Temperature:  req.Temperature,
		Humidity:     req.Humidity,
		Voltage:      req.Voltage,
		InsulationOK: req.InsulationOK,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cabin)
}

type reportAlarmReq struct {
	AlarmType  string `json:"alarm_type"`
	ReporterID string `json:"reporter_id"`
}

func (s *Server) handleReportAlarm(w http.ResponseWriter, r *http.Request) {
	cabinID := r.PathValue("id")
	var req reportAlarmReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var alarmType domain.AlarmType
	switch strings.ToLower(req.AlarmType) {
	case "temperature_over_limit", "temp":
		alarmType = domain.AlarmTempOverLimit
	case "insulation":
		alarmType = domain.AlarmInsulation
	default:
		writeError(w, http.StatusBadRequest, "invalid alarm_type; use temperature_over_limit or insulation")
		return
	}
	result, err := s.svc.ReportAlarm(cabinID, alarmType, req.ReporterID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

// ---------------------------------------------------------------------------
// Inspection endpoints
// ---------------------------------------------------------------------------

type dispatchInspectionReq struct {
	CabinID     string `json:"cabin_id"`
	InspectorID string `json:"inspector_id"`
	PlanWeek    string `json:"plan_week"`
}

func (s *Server) handleDispatchInspection(w http.ResponseWriter, r *http.Request) {
	var req dispatchInspectionReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	order, err := s.svc.DispatchInspection(req.CabinID, req.InspectorID, req.PlanWeek)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *Server) handleStartInspection(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.svc.StartInspection(orderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleRecordReadings(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	var req readingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	order, err := s.svc.RecordInspectionReadings(orderID, domain.Readings{
		Temperature:  req.Temperature,
		Humidity:     req.Humidity,
		Voltage:      req.Voltage,
		InsulationOK: req.InsulationOK,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleCompleteInspection(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.svc.CompleteInspection(orderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

// ---------------------------------------------------------------------------
// Maintenance endpoints
// ---------------------------------------------------------------------------

type dispatchMaintenanceReq struct {
	DispatcherID     string `json:"dispatcher_id"`
	EngineerID       string `json:"engineer_id"`
	BackupEngineerID string `json:"backup_engineer_id"`
}

func (s *Server) handleDispatchMaintenance(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	var req dispatchMaintenanceReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	order, err := s.svc.DispatchMaintenance(orderID, req.DispatcherID, req.EngineerID, req.BackupEngineerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

type engineerReq struct {
	EngineerID string `json:"engineer_id"`
}

func (s *Server) handleEngineerArrive(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	var req engineerReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	order, err := s.svc.EngineerArrive(orderID, req.EngineerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleCompleteProcessing(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	var req engineerReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	order, err := s.svc.CompleteProcessing(orderID, req.EngineerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

type acceptReq struct {
	LeaderID  string `json:"leader_id"`
	StationID string `json:"station_id"`
}

func (s *Server) handleAcceptMaintenance(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	var req acceptReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	order, err := s.svc.AcceptMaintenance(orderID, req.LeaderID, req.StationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleTransferOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.svc.TransferOrder(orderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

type listWorkOrdersResp struct {
	Maintenance []*domain.MaintenanceOrder `json:"maintenance"`
	Inspection  []*domain.InspectionOrder  `json:"inspection"`
}

func (s *Server) handleListWorkOrders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, listWorkOrdersResp{
		Maintenance: s.svc.ListMaintenanceOrders(),
		Inspection:  s.svc.ListInspectionOrders(),
	})
}

// ---------------------------------------------------------------------------
// Inventory endpoints
// ---------------------------------------------------------------------------

type addPartReq struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	Stock      int    `json:"stock"`
	SafetyLine int    `json:"safety_line"`
}

func (s *Server) handleAddPart(w http.ResponseWriter, r *http.Request) {
	var req addPartReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	part, err := s.svc.AddSparePart(req.ID, req.Name, req.Unit, req.Stock, req.SafetyLine)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, part)
}

func (s *Server) handleListParts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.ListSpareParts())
}

type consumeReq struct {
	Quantity int `json:"quantity"`
}

func (s *Server) handleConsumePart(w http.ResponseWriter, r *http.Request) {
	partID := r.PathValue("id")
	var req consumeReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	part, replenishment, err := s.svc.ConsumePart(partID, req.Quantity)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrPartNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, domain.ErrInsufficientStock):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"part":          part,
		"replenishment": replenishment,
	})
}

func (s *Server) handleListReplenishments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.ListReplenishmentRequests())
}

// ---------------------------------------------------------------------------
// Operation endpoints
// ---------------------------------------------------------------------------

type initiateOpReq struct {
	Type         string `json:"type"`
	DispatcherID string `json:"dispatcher_id"`
}

func (s *Server) handleInitiateOperation(w http.ResponseWriter, r *http.Request) {
	var req initiateOpReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var opType domain.OperationType
	switch strings.ToLower(req.Type) {
	case "grid_connect":
		opType = domain.OperationGridConnect
	case "black_start":
		opType = domain.OperationBlackStart
	default:
		writeError(w, http.StatusBadRequest, "invalid type; use grid_connect or black_start")
		return
	}
	op, err := s.svc.InitiateOperation(opType, req.DispatcherID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, op)
}

type confirmOpReq struct {
	StationID string `json:"station_id"`
}

func (s *Server) handleConfirmOperation(w http.ResponseWriter, r *http.Request) {
	opID := r.PathValue("id")
	var req confirmOpReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	op, err := s.svc.ConfirmOperation(opID, req.StationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *Server) handleExecuteOperation(w http.ResponseWriter, r *http.Request) {
	opID := r.PathValue("id")
	op, err := s.svc.ExecuteOperation(opID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, op)
}

type cancelOpReq struct {
	Actor string `json:"actor"`
}

func (s *Server) handleCancelOperation(w http.ResponseWriter, r *http.Request) {
	opID := r.PathValue("id")
	var req cancelOpReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	op, err := s.svc.CancelOperation(opID, req.Actor)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *Server) handleListOperations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.ListOperations())
}
