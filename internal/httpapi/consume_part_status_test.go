package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConsumePartHTTPStatusCodes describes the expected REST status codes of the
// spare-part consumption endpoint.
//
// Input: a part with 2 units on hand receives POST
// /api/inventory/parts/{id}/consume with quantity 5, quantity 0, an unknown part
// id, and finally a valid quantity.
//
// Expected output: a stock shortage answers 409 Conflict, a non-positive
// quantity answers 400 Bad Request, an unknown part answers 404 Not Found, and a
// valid consumption answers 200 OK.
func TestConsumePartHTTPStatusCodes(t *testing.T) {
	server, _ := newTestServer()

	req := httptest.NewRequest("POST", "/api/inventory/parts",
		strings.NewReader(`{"id":"part-bms","name":"BMS Controller","unit":"pcs","stock":2,"safety_line":3}`))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add part: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	cases := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{name: "stock shortage", path: "/api/inventory/parts/part-bms/consume", body: `{"quantity":5}`, status: http.StatusConflict},
		{name: "non-positive quantity", path: "/api/inventory/parts/part-bms/consume", body: `{"quantity":0}`, status: http.StatusBadRequest},
		{name: "unknown part", path: "/api/inventory/parts/part-nope/consume", body: `{"quantity":1}`, status: http.StatusNotFound},
		{name: "valid consumption", path: "/api/inventory/parts/part-bms/consume", body: `{"quantity":1}`, status: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, w.Code, w.Body.String())
			}
		})
	}
}
