package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kamini/latency-intelligence/internal/config"
	"github.com/kamini/latency-intelligence/internal/regression"
	"github.com/kamini/latency-intelligence/internal/store"
)

type Handler struct {
	store    *store.ClickHouseStore
	detector *regression.Detector
	cfg      *config.Config
}

func NewHandler(
	st *store.ClickHouseStore,
	det *regression.Detector,
	cfg *config.Config,
) *Handler {
	return &Handler{
		store:    st,
		detector: det,
		cfg:      cfg,
	}
}

// ─────────────────────────────────────────────────────────────
// GET /calibrate
// Called by the IntelliJ plugin to fetch real measured p99.
// ─────────────────────────────────────────────────────────────
//
// Query params:
//
//	method       → fully qualified method name (required)
//	service      → service name                (required)
//	environment  → prod | staging | dev       (default: prod)
//	window       → lookback window            (default: 24h)
//
// Example:
//
//	/calibrate?method=com.kamini.OrderService.placeOrder
//	  &service=order-svc
//	  &environment=staging
//	  &window=24h
func (h *Handler) Calibrate(
	w http.ResponseWriter,
	r *http.Request,
) {
	method := r.URL.Query().Get("method")
	service := r.URL.Query().Get("service")
	env := r.URL.Query().Get("environment")
	windowS := r.URL.Query().Get("window")

	if method == "" || service == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"method and service are required",
		)
		return
	}

	if env == "" {
		env = "prod"
	}

	window := 24 * time.Hour

	if windowS != "" {
		if d, err := time.ParseDuration(windowS); err == nil {
			window = d
		}
	}

	stats, err := h.store.GetLatencyStats(
		r.Context(),
		method,
		env,
		service,
		window,
	)

	if err != nil {
		writeError(
			w,
			http.StatusNotFound,
			"no data found for method '"+method+
				"' in environment '"+env+"'",
		)
		return
	}

	// Warn if sample size is too low
	if stats.SampleCount < int64(h.cfg.MinSampleCount) {

		writeJSON(
			w,
			http.StatusPartialContent,
			map[string]any{
				"warning":      "insufficient samples — estimates may be inaccurate",
				"sample_count": stats.SampleCount,
				"min_required": h.cfg.MinSampleCount,
				"data":         stats,
			},
		)

		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// ─────────────────────────────────────────────────────────────
// POST /regression
// Detect regressions between two commits.
// Used by CI pipelines.
// ─────────────────────────────────────────────────────────────
//
// Example payload:
//
//	{
//	  "environment": "staging",
//	  "baseline_commit": "abc123",
//	  "candidate_commit": "def567",
//	  "methods": [
//	    "com.kamini.OrderService.placeOrder"
//	  ]
//	}
type regressionRequest struct {
	Environment     string   `json:"environment"`
	BaselineCommit  string   `json:"baseline_commit"`
	CandidateCommit string   `json:"candidate_commit"`
	Methods         []string `json:"methods"`
}

func (h *Handler) DetectRegression(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req regressionRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"invalid JSON: "+err.Error(),
		)
		return
	}

	if req.BaselineCommit == "" ||
		req.CandidateCommit == "" ||
		len(req.Methods) == 0 {

		writeError(
			w,
			http.StatusBadRequest,
			"baseline_commit, candidate_commit and methods are required",
		)

		return
	}

	if req.Environment == "" {
		req.Environment = "staging"
	}

	reports, err := h.detector.CompareAll(
		r.Context(),
		req.Environment,
		req.BaselineCommit,
		req.CandidateCommit,
		req.Methods,
	)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err.Error(),
		)
		return
	}

	// Count regressions
	regressionCount := 0

	for _, rep := range reports {
		if rep.IsRegression {
			regressionCount++
		}
	}

	status := http.StatusOK

	// Fail CI if regressions detected
	if regressionCount > 0 {
		status = http.StatusUnprocessableEntity
	}

	writeJSON(
		w,
		status,
		map[string]any{
			"regression_count": regressionCount,
			"total_checked":    len(reports),
			"threshold_pct":    h.cfg.RegressionThresholdPct * 100,
			"reports":          reports,
		},
	)
}

// ─────────────────────────────────────────────────────────────
// GET /hotspots
// Returns top slowest methods for dashboard/team view.
// ─────────────────────────────────────────────────────────────
//
// Query params:
//
//	service      → service name          (required)
//	environment  → prod | staging | dev (default: prod)
//	limit        → result count          (default: 10)
//	window       → lookback window       (default: 24h)
func (h *Handler) TopHotspots(
	w http.ResponseWriter,
	r *http.Request,
) {
	service := r.URL.Query().Get("service")
	env := r.URL.Query().Get("environment")
	limitS := r.URL.Query().Get("limit")
	windowS := r.URL.Query().Get("window")

	if service == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"service is required",
		)
		return
	}

	if env == "" {
		env = "prod"
	}

	limit := 10

	if limitS != "" {
		if l, err := strconv.Atoi(limitS); err == nil && l > 0 {
			limit = l
		}
	}

	window := 24 * time.Hour

	if windowS != "" {
		if d, err := time.ParseDuration(windowS); err == nil {
			window = d
		}
	}

	hotspots, err := h.store.GetTopHotspots(
		r.Context(),
		env,
		service,
		limit,
		window,
	)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err.Error(),
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		map[string]any{
			"environment": env,
			"service":     service,
			"window":      window.String(),
			"count":       len(hotspots),
			"hotspots":    hotspots,
		},
	)
}

// ─────────────────────────────────────────────────────────────
// GET /health
// Basic health endpoint.
// ─────────────────────────────────────────────────────────────
func (h *Handler) Health(
	w http.ResponseWriter,
	r *http.Request,
) {
	writeJSON(
		w,
		http.StatusOK,
		map[string]string{
			"status":  "ok",
			"service": "latency-intelligence",
		},
	)
}

// ─────────────────────────────────────────────────────────────
// POST /webhook/ci
// Triggered by GitHub Actions / GitLab CI.
// Reuses regression detection logic.
// ─────────────────────────────────────────────────────────────
func (h *Handler) CIWebhook(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.DetectRegression(w, r)
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────
func writeJSON(
	w http.ResponseWriter,
	status int,
	v any,
) {
	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

func writeError(
	w http.ResponseWriter,
	status int,
	msg string,
) {
	writeJSON(
		w,
		status,
		map[string]string{
			"error": msg,
		},
	)
}

// Unused import guard
var _ = chi.URLParam
