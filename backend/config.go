package main

import (
	"os"
	"strconv"
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

var (
	port             = getEnvInt("BACKEND_PORT", 3001)
	walrusPublisher  = getEnv("WALRUS_PUBLISHER", "https://publisher.walrus-testnet.walrus.space")
	walrusAggregator = getEnv("WALRUS_AGGREGATOR", "https://aggregator.walrus-testnet.walrus.space")
	epochs           = getEnvInt("WALRUS_EPOCHS", 5)
	corsOrigin       = getEnv("CORS_ORIGIN", "http://localhost:5173")
	mongoURI         = getEnv("MONGODB_URI", "mongodb://localhost:27017")
	mongoDatabase    = getEnv("MONGODB_DATABASE", "debox")
	metricsPort      = getEnvInt("METRICS_PORT", 9090)
	googleClientID   = os.Getenv("GOOGLE_CLIENT_ID")
)
