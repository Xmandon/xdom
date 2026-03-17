package app

import "os"

type Config struct {
	ServiceName string
	Environment string
	Version     string
	CommitSHA   string
	BuildID     string
	ListenAddr  string
	AdminToken  string
}

func LoadConfigFromEnv() Config {
	return Config{
		ServiceName: getEnv("SERVICE_NAME", "xdom"),
		Environment: getEnv("ENVIRONMENT", "dev"),
		Version:     getEnv("VERSION", "0.1.0"),
		CommitSHA:   getEnv("COMMIT_SHA", "unknown"),
		BuildID:     getEnv("BUILD_ID", "local"),
		ListenAddr:  getEnv("LISTEN_ADDR", ":8080"),
		AdminToken:  getEnv("ADMIN_TOKEN", "xdom-admin-token"),
	}
}

func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
