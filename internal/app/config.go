package app

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServiceName       string
	Environment       string
	Version           string
	CommitSHA         string
	BuildID           string
	ListenAddr        string
	AdminToken        string
	DBPath            string
	LogLevel          string
	Token             string
	OTLPEndpoint      string
	EnableTraces      bool
	EnableMetrics     bool
	EnableLogs        bool
	NetHostIP         string
	PaymentLatencyMS  int
	OrderTimeoutSec   int
	WorkerIntervalSec int
}

func LoadConfigFromEnv() Config {
	return Config{
		ServiceName:       getEnv("SERVICE_NAME", "xdom"),
		Environment:       getEnv("ENVIRONMENT", "dev"),
		Version:           getEnv("VERSION", "0.1.0"),
		CommitSHA:         getEnv("COMMIT_SHA", "unknown"),
		BuildID:           getEnv("BUILD_ID", "local"),
		ListenAddr:        getEnv("LISTEN_ADDR", ":8080"),
		AdminToken:        getEnv("ADMIN_TOKEN", "xdom-admin-token"),
		DBPath:            getEnv("DB_PATH", "data/xdom.db"),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		Token:             getEnv("TOKEN", ""),
		OTLPEndpoint:      trimScheme(getEnv("OTLP_ENDPOINT", "")),
		EnableTraces:      getBoolEnv("ENABLE_TRACES", true),
		EnableMetrics:     getBoolEnv("ENABLE_METRICS", true),
		EnableLogs:        getBoolEnv("ENABLE_LOGS", true),
		NetHostIP:         getEnv("NET_HOST_IP", ""),
		PaymentLatencyMS:  getIntEnv("PAYMENT_LATENCY_MS", 150),
		OrderTimeoutSec:   getIntEnv("ORDER_TIMEOUT_SEC", 30),
		WorkerIntervalSec: getIntEnv("WORKER_INTERVAL_SEC", 10),
	}
}

func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getIntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func trimScheme(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	return endpoint
}
