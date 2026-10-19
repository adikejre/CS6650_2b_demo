package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validBody = `{"product_id":1,"sku":"ABC-123-XYZ","manufacturer":"Acme Corporation","category_id":456,"weight":0,"some_other_id":789}`

func TestProductEndpoints(t *testing.T) {
	store := NewProductStore()
	store.Seed(10)
	r := newRouter(store, slog.New(slog.NewJSONHandler(io.Discard, nil)), true)

	cases := []struct {
		name, method, path, body string
		fault                    bool
		want                     int
	}{
		{"GET existing", "GET", "/products/1", "", false, 200},
		{"GET missing", "GET", "/products/999", "", false, 404},
		{"GET non-integer id", "GET", "/products/abc", "", false, 404},
		{"GET injected fault", "GET", "/products/1", "", true, 500},
		{"POST valid", "POST", "/products/1/details", validBody, false, 204},
		{"POST missing product", "POST", "/products/999/details",
			strings.Replace(validBody, `"product_id":1`, `"product_id":999`, 1), false, 404},
		{"POST bad path id", "POST", "/products/0/details", validBody, false, 400},
		{"POST malformed json", "POST", "/products/1/details", `{"sku":`, false, 400},
		{"POST empty body", "POST", "/products/1/details", "", false, 400},
		{"POST missing field", "POST", "/products/1/details", `{"product_id":1,"sku":"x"}`, false, 400},
		{"POST wrong type", "POST", "/products/1/details",
			strings.Replace(validBody, `"weight":0`, `"weight":"heavy"`, 1), false, 400},
		{"POST negative weight", "POST", "/products/1/details",
			strings.Replace(validBody, `"weight":0`, `"weight":-1`, 1), false, 400},
		{"POST empty sku", "POST", "/products/1/details",
			strings.Replace(validBody, `"ABC-123-XYZ"`, `""`, 1), false, 400},
		{"POST id mismatch", "POST", "/products/2/details", validBody, false, 400},
		{"POST injected fault", "POST", "/products/1/details", validBody, true, 500},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.fault {
				req.Header.Set("X-Fault-Inject", "500")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want != http.StatusNoContent && tc.want != http.StatusOK &&
				!strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("error response missing Error schema: %s", w.Body.String())
			}
		})
	}

	// The successful POST should be visible through GET.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/products/1", nil))
	if !strings.Contains(w.Body.String(), `"sku":"ABC-123-XYZ"`) {
		t.Fatalf("update not persisted: %s", w.Body.String())
	}
}
