package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv                    string
	HTTPAddr                  string
	LogLevel                  string
	DatabaseURL               string
	RedisAddr                 string
	RedisPassword             string
	RedisDB                   int
	CookieSecure              bool
	VisitorSessionTTL         time.Duration
	ReservationTTL            time.Duration
	ExpirationWorkerInterval  time.Duration
	ExpirationWorkerBatchSize int32
	StartupTimeout            time.Duration
	ReadinessTimeout          time.Duration
	ShutdownTimeout           time.Duration
}

func Load() (Config, error) {
	redisDB, err := getenvInt("REDIS_DB", 0)
	if err != nil {
		return Config{}, err
	}

	startupTimeout, err := getenvDuration("STARTUP_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	readinessTimeout, err := getenvDuration("READINESS_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := getenvDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	visitorSessionTTL, err := getenvDuration("VISITOR_SESSION_TTL", 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cookieSecure, err := getenvBool("COOKIE_SECURE", false)
	if err != nil {
		return Config{}, err
	}
	reservationTTL, err := getenvDuration("RESERVATION_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	expirationWorkerInterval, err := getenvDuration("EXPIRATION_WORKER_INTERVAL", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	expirationWorkerBatchSize, err := getenvInt("EXPIRATION_WORKER_BATCH_SIZE", 100)
	if err != nil {
		return Config{}, err
	}
	if expirationWorkerBatchSize <= 0 {
		return Config{}, errors.New("EXPIRATION_WORKER_BATCH_SIZE must be greater than zero")
	}

	cfg := Config{
		AppEnv:                    getenv("APP_ENV", "development"),
		HTTPAddr:                  getenv("HTTP_ADDR", ":8080"),
		LogLevel:                  getenv("LOG_LEVEL", "INFO"),
		DatabaseURL:               os.Getenv("DATABASE_URL"),
		RedisAddr:                 os.Getenv("REDIS_ADDR"),
		RedisPassword:             os.Getenv("REDIS_PASSWORD"),
		RedisDB:                   redisDB,
		CookieSecure:              cookieSecure,
		VisitorSessionTTL:         visitorSessionTTL,
		ReservationTTL:            reservationTTL,
		ExpirationWorkerInterval:  expirationWorkerInterval,
		ExpirationWorkerBatchSize: int32(expirationWorkerBatchSize),
		StartupTimeout:            startupTimeout,
		ReadinessTimeout:          readinessTimeout,
		ShutdownTimeout:           shutdownTimeout,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.RedisAddr == "" {
		return Config{}, errors.New("REDIS_ADDR is required")
	}

	return cfg, nil
}

func ConfigureLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToUpper(level) {
	case "DEBUG":
		slogLevel = slog.LevelDebug
	case "WARN":
		slogLevel = slog.LevelWarn
	case "ERROR":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel,
	}))
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}
	return duration, nil
}

func getenvInt(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer: %w", key, err)
	}
	return parsed, nil
}

func getenvBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a valid boolean: %w", key, err)
	}
	return parsed, nil
}
