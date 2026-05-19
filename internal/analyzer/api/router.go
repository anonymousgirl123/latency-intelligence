package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func NewRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()

	// ─────────────────────────────────────────────────────────────
	// Middleware
	// ─────────────────────────────────────────────────────────────
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"*", // tighten in production
		},
		AllowedMethods: []string{
			"GET",
			"POST",
			"OPTIONS",
		},
		AllowedHeaders: []string{
			"Accept",
			"Content-Type",
			"Authorization",
		},
	}))

	// ─────────────────────────────────────────────────────────────
	// Health
	// ─────────────────────────────────────────────────────────────
	r.Get("/health", h.Health)

	// ─────────────────────────────────────────────────────────────
	// Plugin integration endpoints
	// ─────────────────────────────────────────────────────────────

	// GET real measured p99 for a method
	r.Get("/calibrate", h.Calibrate)

	// GET top slowest methods
	r.Get("/hotspots", h.TopHotspots)

	// ─────────────────────────────────────────────────────────────
	// Regression detection
	// Called by CI pipeline or IntelliJ plugin
	// ─────────────────────────────────────────────────────────────
	r.Post("/regression", h.DetectRegression)

	// ─────────────────────────────────────────────────────────────
	// CI webhook
	// Same regression logic, separate endpoint
	// ─────────────────────────────────────────────────────────────
	r.Post("/webhook/ci", h.CIWebhook)

	return r
}
