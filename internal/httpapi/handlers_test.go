package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"batteryops/internal/domain"
	"batteryops/internal/notify"
	"batteryops/internal/service"
	"batteryops/internal/store"
)

func newTestServer() (*Server, *store.Store) {
	st := store.New()
	n := notify.NewSMSNotifier()
	svc := service.New(st, n, service.DefaultConfig())
	return NewServer(svc), st
}

// TestHTTPHealth verifies the health endpoint.
func TestHTTPHealth(t *testing.T) {
	server, _ := newTestServer()
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestHTTPAlarmReportAndDispatch exercises the full HTTP flow: register cabin,
// report alarm, dispatch maintenance, and verify the cabin is locked.
func TestHTTPAlarmReportAndDispatch(t *testing.T) {
	server, st := newTestServer()

	// Register cabin.
	body := `{"id":"cabin-1","location":"Site A","temp_limit":45.0}`
	req := httptest.NewRequest("POST", "/api/cabins", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register cabin: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Report alarm.
	body = `{"alarm_type":"temperature_over_limit","reporter_id":"inspector-1"}`
	req = httptest.NewRequest("POST", "/api/cabins/cabin-1/alarm", strings.NewReader(body))
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("report alarm: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var result service.ReportAlarmResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode alarm result: %v", err)
	}
	if !result.Created {
		t.Fatal("expected alarm to create order")
	}

	// Dispatch maintenance.
	body = `{"dispatcher_id":"dispatcher-1","engineer_id":"eng-1","backup_engineer_id":"eng-2"}`
	req = httptest.NewRequest("POST", "/api/workorders/maintenance/"+result.Order.ID+"/dispatch", strings.NewReader(body))
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dispatch: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify cabin is locked.
	cabin, _ := st.GetCabin("cabin-1")
	if cabin.Status != domain.CabinLocked {
		t.Fatalf("expected cabin locked, got %s", cabin.Status)
	}
}

// TestHTTPDuplicateAlarm verifies that a second alarm report for the same
// cabin returns 200 with created=false and a confirmation prompt.
func TestHTTPDuplicateAlarm(t *testing.T) {
	server, _ := newTestServer()

	// Register cabin.
	body := `{"id":"cabin-2","location":"Site B","temp_limit":45.0}`
	req := httptest.NewRequest("POST", "/api/cabins", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	// First alarm report.
	body = `{"alarm_type":"insulation","reporter_id":"inspector-A"}`
	req = httptest.NewRequest("POST", "/api/cabins/cabin-2/alarm", strings.NewReader(body))
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first alarm: expected 201, got %d", w.Code)
	}

	// Second alarm report from a different inspector.
	body = `{"alarm_type":"temperature_over_limit","reporter_id":"inspector-B"}`
	req = httptest.NewRequest("POST", "/api/cabins/cabin-2/alarm", strings.NewReader(body))
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("duplicate alarm: expected 200, got %d", w.Code)
	}

	var result service.ReportAlarmResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Created {
		t.Fatal("expected second alarm to not create a new order")
	}
	if !strings.Contains(result.Message, "confirm") {
		t.Fatalf("expected message to contain 'confirm', got: %s", result.Message)
	}
}

// TestHTTPOperationDualConfirm verifies the full dual-confirmation flow for a
// grid-connect operation via HTTP.
func TestHTTPOperationDualConfirm(t *testing.T) {
	server, _ := newTestServer()

	// Initiate operation.
	body := `{"type":"grid_connect","dispatcher_id":"dispatcher-1"}`
	req := httptest.NewRequest("POST", "/api/operations", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("initiate: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var op domain.Operation
	if err := json.NewDecoder(w.Body).Decode(&op); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Execute without station confirmation should fail.
	req = httptest.NewRequest("POST", "/api/operations/"+op.ID+"/execute", nil)
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("execute without confirm: expected 409, got %d", w.Code)
	}

	// Confirm by station.
	body = `{"station_id":"station-1"}`
	req = httptest.NewRequest("POST", "/api/operations/"+op.ID+"/confirm", strings.NewReader(body))
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Execute should now succeed.
	req = httptest.NewRequest("POST", "/api/operations/"+op.ID+"/execute", nil)
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	json.NewDecoder(w.Body).Decode(&op)
	if op.Status != domain.OperationExecuted {
		t.Fatalf("expected executed, got %s", op.Status)
	}
}

// TestHTTPConsumePartReplenishment verifies that consuming a part below the
// safety line triggers a replenishment request via HTTP.
func TestHTTPConsumePartReplenishment(t *testing.T) {
	server, _ := newTestServer()

	// Add a part.
	body := `{"id":"part-1","name":"Fuse","unit":"pcs","stock":3,"safety_line":5}`
	req := httptest.NewRequest("POST", "/api/inventory/parts", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add part: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Consume 1 (stock goes to 2, below safety line 5).
	body = `{"quantity":1}`
	req = httptest.NewRequest("POST", "/api/inventory/parts/part-1/consume", strings.NewReader(body))
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("consume: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Part          *domain.SparePart            `json:"part"`
		Replenishment *domain.ReplenishmentRequest `json:"replenishment"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Part.Stock != 2 {
		t.Fatalf("expected stock 2, got %d", resp.Part.Stock)
	}
	if resp.Replenishment == nil {
		t.Fatal("expected replenishment request")
	}

	// List replenishments.
	req = httptest.NewRequest("GET", "/api/inventory/replenishments", nil)
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list replenishments: expected 200, got %d", w.Code)
	}
}
