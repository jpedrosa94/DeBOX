package main

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, route, and status code.",
		},
		[]string{"method", "route", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	walrusUploadTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "walrus_upload_total",
			Help: "Total Walrus upload attempts by outcome.",
		},
		[]string{"outcome"},
	)

	walrusUploadDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "walrus_upload_duration_seconds",
			Help:    "Duration of Walrus upload requests in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)
)

// normalizePath maps raw URLs to route templates to avoid high-cardinality labels.
func normalizePath(rawPath string) string {
	switch {
	case rawPath == "/health":
		return "/health"
	case rawPath == "/api/upload":
		return "/api/upload"
	case strings.HasPrefix(rawPath, "/api/blob/"):
		return "/api/blob/{blobId}"
	case strings.HasPrefix(rawPath, "/api/files/"):
		parts := strings.Split(strings.TrimPrefix(rawPath, "/api/files/"), "/")
		if len(parts) <= 1 {
			return "/api/files/{address}"
		}
		return "/api/files/{address}/{blobId}"
	default:
		return rawPath
	}
}
