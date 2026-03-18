package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ─── MongoDB collections ─────────────────────────────────────────────────────

var (
	filesCol *mongo.Collection
	usersCol *mongo.Collection
)

func main() {
	initLogger()

	shutdownTracer := initTracer(context.Background(), otelEndpoint)
	defer shutdownTracer(context.Background())

	// Connect to MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI).SetMonitor(newMongoMonitor()))
	if err != nil {
		slog.Error("failed to connect to MongoDB", "error", err)
		os.Exit(1)
	}
	defer client.Disconnect(context.Background())

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		slog.Error("failed to ping MongoDB", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to MongoDB", "uri", mongoURI)

	db := client.Database(mongoDatabase)
	filesCol = db.Collection("files")
	usersCol = db.Collection("users")

	// Create indexes
	filesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "address", Value: 1}, {Key: "uploadedAt", Value: -1}},
	})
	usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "sub", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "address", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/upload", authMiddleware(handleUpload))
	mux.HandleFunc("GET /api/blob/{blobId}", handleGetBlob) // no auth — content-addressed
	mux.HandleFunc("GET /api/files/{address}", authMiddleware(handleListFiles))
	mux.HandleFunc("POST /api/files/{address}", authMiddleware(handleSaveFile))
	mux.HandleFunc("DELETE /api/files/{address}/{blobId}", authMiddleware(handleDeleteFile))
	mux.HandleFunc("GET /health", handleHealth)

	addr := fmt.Sprintf(":%d", port)
	slog.Info("backend starting", "addr", addr, "walrus_publisher", walrusPublisher)
	if googleClientID == "" {
		slog.Warn("GOOGLE_CLIENT_ID not set, JWT audience verification disabled")
	}

	// Start metrics server on separate port
	go func() {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("GET /metrics", promhttp.Handler())
		metricsAddr := fmt.Sprintf(":%d", metricsPort)
		slog.Info("metrics server starting", "addr", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, metricsMux); err != nil {
			slog.Error("metrics server failed", "error", err)
		}
	}()

	handler := otelhttp.NewHandler(loggingHandler{next: corsHandler{mux}}, "debox-backend")
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
