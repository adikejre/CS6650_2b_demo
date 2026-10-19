// product-service implements the Product endpoints of api.yaml:
//
//	GET  /products/{productId}          -> 200 | 404 | 500
//	POST /products/{productId}/details  -> 204 | 400 | 404 | 500
//
// Products live in an in-memory map guarded by a sync.RWMutex. The API has no
// "create product" endpoint and POST returns 404 for unknown products, so the
// store is seeded with a catalog of products at startup (SEED_PRODUCTS) and
// POST adds/updates the details of an existing product.
//
// Observability: every request is counted and timed into Prometheus metrics
// (exposed at /metrics) and emits one structured JSON log line.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------------------------------------------------------------------------
// Schemas (components/schemas in api.yaml)
// ---------------------------------------------------------------------------

// Product matches components/schemas/Product.
type Product struct {
	ProductID    int32  `json:"product_id"`
	SKU          string `json:"sku"`
	Manufacturer string `json:"manufacturer"`
	CategoryID   int32  `json:"category_id"`
	Weight       int32  `json:"weight"`
	SomeOtherID  int32  `json:"some_other_id"`
}

// productInput is the POST body. Pointer fields let us tell "missing" apart
// from a legitimate zero value (weight: 0 is valid, but weight must be present).
// Using int32 means out-of-range numbers and non-integers fail to decode.
type productInput struct {
	ProductID    *int32  `json:"product_id"`
	SKU          *string `json:"sku"`
	Manufacturer *string `json:"manufacturer"`
	CategoryID   *int32  `json:"category_id"`
	Weight       *int32  `json:"weight"`
	SomeOtherID  *int32  `json:"some_other_id"`
}

// APIError matches components/schemas/Error.
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func abortWithError(c *gin.Context, status int, code, message, details string) {
	c.AbortWithStatusJSON(status, APIError{Error: code, Message: message, Details: details})
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// ProductStore is a map keyed by product ID: O(1) lookups for the read-heavy
// GET path. Gin serves each request on its own goroutine, so the map needs a
// lock — an RWMutex lets many GETs read concurrently while POSTs take the
// exclusive write lock.
type ProductStore struct {
	mu       sync.RWMutex
	products map[int32]Product
}

func NewProductStore() *ProductStore {
	return &ProductStore{products: make(map[int32]Product)}
}

func (s *ProductStore) Get(id int32) (Product, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.products[id]
	return p, ok
}

// Update replaces the details of an existing product. It returns false if the
// product does not exist.
func (s *ProductStore) Update(p Product) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.products[p.ProductID]; !ok {
		return false
	}
	s.products[p.ProductID] = p
	return true
}

// Seed inserts products 1..n with placeholder details.
func (s *ProductStore) Seed(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 1; i <= n; i++ {
		id := int32(i)
		s.products[id] = Product{
			ProductID:    id,
			SKU:          fmt.Sprintf("SKU-%06d", i),
			Manufacturer: "Acme Corporation",
			CategoryID:   int32(i%50 + 1),
			Weight:       int32(i % 5000),
			SomeOtherID:  int32(i%1000 + 1),
		}
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type ProductHandler struct {
	store *ProductStore
}

// parseProductID validates the {productId} path parameter (int32, minimum 1).
func parseProductID(raw string) (int32, error) {
	id, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("productId must be a 32-bit integer, got %q", raw)
	}
	if id < 1 {
		return 0, fmt.Errorf("productId must be >= 1, got %d", id)
	}
	return int32(id), nil
}

// GetProduct handles GET /products/{productId}.
func (h *ProductHandler) GetProduct(c *gin.Context) {
	id, err := parseProductID(c.Param("productId"))
	if err != nil {
		// The GET contract only defines 200/404/500, so a malformed ID is
		// reported as "not found": no product can exist under that ID.
		abortWithError(c, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found", err.Error())
		return
	}

	p, ok := h.store.Get(id)
	if !ok {
		abortWithError(c, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found",
			fmt.Sprintf("no product with productId %d", id))
		return
	}
	c.JSON(http.StatusOK, p)
}

// AddProductDetails handles POST /products/{productId}/details.
func (h *ProductHandler) AddProductDetails(c *gin.Context) {
	id, err := parseProductID(c.Param("productId"))
	if err != nil {
		abortWithError(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid productId", err.Error())
		return
	}

	var in productInput
	if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
		details := err.Error()
		if errors.Is(err, io.EOF) {
			details = "request body is required"
		}
		abortWithError(c, http.StatusBadRequest, "INVALID_INPUT", "Request body is not a valid Product", details)
		return
	}

	p, err := validateProduct(in)
	if err != nil {
		abortWithError(c, http.StatusBadRequest, "INVALID_INPUT", "The provided input data is invalid", err.Error())
		return
	}
	if p.ProductID != id {
		abortWithError(c, http.StatusBadRequest, "INVALID_INPUT", "The provided input data is invalid",
			fmt.Sprintf("body product_id %d does not match path productId %d", p.ProductID, id))
		return
	}

	if !h.store.Update(p) {
		abortWithError(c, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found",
			fmt.Sprintf("no product with productId %d", id))
		return
	}
	c.Status(http.StatusNoContent)
}

// validateProduct enforces the required fields and constraints of the Product schema.
func validateProduct(in productInput) (Product, error) {
	switch {
	case in.ProductID == nil:
		return Product{}, errors.New("product_id is required")
	case *in.ProductID < 1:
		return Product{}, errors.New("product_id must be >= 1")
	case in.SKU == nil:
		return Product{}, errors.New("sku is required")
	case len(*in.SKU) < 1 || len(*in.SKU) > 100:
		return Product{}, errors.New("sku must be 1-100 characters")
	case in.Manufacturer == nil:
		return Product{}, errors.New("manufacturer is required")
	case len(*in.Manufacturer) < 1 || len(*in.Manufacturer) > 200:
		return Product{}, errors.New("manufacturer must be 1-200 characters")
	case in.CategoryID == nil:
		return Product{}, errors.New("category_id is required")
	case *in.CategoryID < 1:
		return Product{}, errors.New("category_id must be >= 1")
	case in.Weight == nil:
		return Product{}, errors.New("weight is required")
	case *in.Weight < 0:
		return Product{}, errors.New("weight must be >= 0")
	case in.SomeOtherID == nil:
		return Product{}, errors.New("some_other_id is required")
	case *in.SomeOtherID < 1:
		return Product{}, errors.New("some_other_id must be >= 1")
	}
	return Product{
		ProductID:    *in.ProductID,
		SKU:          *in.SKU,
		Manufacturer: *in.Manufacturer,
		CategoryID:   *in.CategoryID,
		Weight:       *in.Weight,
		SomeOtherID:  *in.SomeOtherID,
	}, nil
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// recovery turns a panic in any handler into a 500 with the Error schema.
func recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, rec any) {
		abortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error",
			fmt.Sprint(rec))
	})
}

// faultInjection lets you trigger the 500 path on purpose (for demos and
// testing) by sending "X-Fault-Inject: 500". It is only installed when
// ENABLE_FAULT_INJECTION=true, so it is off by default and in AWS.
func faultInjection() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-Fault-Inject") == "500" {
			panic("fault injected via X-Fault-Inject header")
		}
		c.Next()
	}
}

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests (Rate).",
	}, []string{"method", "route", "status"})

	httpErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total",
		Help: "HTTP requests that returned a non-2xx status (Errors).",
	}, []string{"method", "route", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency (Duration).",
		Buckets: []float64{.0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"method", "route"})
)

// observe records RED metrics and writes one JSON log line per request. It
// labels by route template (/products/:productId), not raw path, so label
// cardinality stays bounded.
func observe(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		elapsed := time.Since(start)

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		if route == "/metrics" {
			return // don't count Prometheus' own scrapes
		}
		status := c.Writer.Status()
		statusStr := strconv.Itoa(status)
		method := c.Request.Method

		httpRequests.WithLabelValues(method, route, statusStr).Inc()
		if status < 200 || status > 299 {
			httpErrors.WithLabelValues(method, route, statusStr).Inc()
		}
		httpDuration.WithLabelValues(method, route).Observe(elapsed.Seconds())

		logger.Info("request",
			"method", method,
			"endpoint", route,
			"path", c.Request.URL.Path,
			"status", status,
			"duration_ms", float64(elapsed.Microseconds())/1000.0,
			"client_ip", c.ClientIP(),
		)
	}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func newRouter(store *ProductStore, logger *slog.Logger, faults bool) *gin.Engine {
	r := gin.New()
	r.Use(observe(logger), recovery())
	if faults {
		r.Use(faultInjection())
	}

	h := &ProductHandler{store: store}
	r.GET("/products/:productId", h.GetProduct)
	r.POST("/products/:productId/details", h.AddProductDetails)

	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	return r
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	seed := envInt("SEED_PRODUCTS", 1000)
	store := NewProductStore()
	store.Seed(seed)

	faults := os.Getenv("ENABLE_FAULT_INJECTION") == "true"
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // must match container_port in terraform/variables.tf
	}

	logger.Info("starting product service", "port", port, "seeded_products", seed, "fault_injection", faults)
	if err := newRouter(store, logger, faults).Run(":" + port); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}
