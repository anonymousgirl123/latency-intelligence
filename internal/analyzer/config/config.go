package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {

	// Server
	HTTPPort string
	GRPCPort string // OTLP receiver port

	// ClickHouse
	ClickHouseAddr     string
	ClickHouseDB       string
	ClickHouseUser     string
	ClickHousePassword string

	// Redis
	RedisAddr     string
	RedisPassword string

	// Regression thresholds
	RegressionThresholdPct float64 // e.g. 0.20 = alert if p99 increases >20%
	MinSampleCount         int     // minimum spans needed before reporting p99
}

func Load() *Config {

	// Load .env file if present (ignored in production)
	_ = godotenv.Load()

	cfg := &Config{
		HTTPPort:               getEnv("HTTP_PORT", "8080"),
		GRPCPort:               getEnv("GRPC_PORT", "4317"),
		ClickHouseAddr:         getEnv("CLICKHOUSE_ADDR", "localhost:9000"),
		ClickHouseDB:           getEnv("CLICKHOUSE_DB", "latency"),
		ClickHouseUser:         getEnv("CLICKHOUSE_USER", "default"),
		ClickHousePassword:     getEnv("CLICKHOUSE_PASSWORD", ""),
		RedisAddr:              getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:          getEnv("REDIS_PASSWORD", ""),
		RegressionThresholdPct: getEnvFloat("REGRESSION_THRESHOLD_PCT", 0.20),
		MinSampleCount:         getEnvInt("MIN_SAMPLE_COUNT", 30),
	}

	log.Printf(
		"[config] HTTP :%s | gRPC :%s | ClickHouse %s | Redis %s",
		cfg.HTTPPort,
		cfg.GRPCPort,
		cfg.ClickHouseAddr,
		cfg.RedisAddr,
	)

	return cfg
}

func getEnv(key, fallback string) string {

	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {

	if v := os.Getenv(key); v != "" {

		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}

	return fallback
}

func getEnvInt(key string, fallback int) int {

	if v := os.Getenv(key); v != "" {

		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}

	return fallback
}
