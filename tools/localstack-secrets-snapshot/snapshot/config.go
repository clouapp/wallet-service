package snapshot

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables and defaults, unchanged from the Python tool so the
// docker-compose environment keeps working.
const (
	EnvRegion       = "AWS_DEFAULT_REGION"
	EnvAccountID    = "SECRETS_SNAPSHOT_ACCOUNT_ID"
	EnvDir          = "SECRETS_SNAPSHOT_DIR"
	EnvKeyFile      = "SECRETS_SNAPSHOT_KEY_FILE"
	EnvInterval     = "SECRETS_SNAPSHOT_INTERVAL"
	EnvKeep         = "SECRETS_SNAPSHOT_KEEP"
	EnvForce        = "SECRETS_SNAPSHOT_FORCE"
	EnvEndpointURL  = "SECRETS_SNAPSHOT_ENDPOINT_URL"
	forceEnabledVal = "1"

	DefaultEndpointURL     = "http://localhost:4566"
	DefaultRegion          = "us-east-1"
	DefaultAccountID       = "000000000000"
	DefaultDir             = "/var/lib/localstack/secrets-snapshot"
	DefaultKeyFile         = "/run/secrets/localstack-seed/seed.key"
	DefaultIntervalSeconds = 60
	DefaultKeep            = 20

	DefaultLoopLock       = "/tmp/secrets-snapshot-loop.lock"
	DefaultRestoredMarker = "/tmp/secrets-snapshot.restored"
	DefaultProcStat       = "/proc/1/stat"

	minIntervalSeconds = 1
	minKeep            = 1
)

// Config holds every path and knob of the tool.
type Config struct {
	EndpointURL    string
	Region         string
	AccountID      string
	Dir            string
	KeyFile        string
	Interval       time.Duration
	Keep           int
	Force          bool
	LoopLock       string
	RestoredMarker string
	ProcStat       string
}

// ConfigFromEnv reads the same environment variables as the Python tool.
func ConfigFromEnv() (Config, error) {
	interval, err := positiveIntEnv(EnvInterval, DefaultIntervalSeconds, minIntervalSeconds)
	if err != nil {
		return Config{}, err
	}
	keep, err := positiveIntEnv(EnvKeep, DefaultKeep, minKeep)
	if err != nil {
		return Config{}, err
	}
	return Config{
		EndpointURL:    stringEnv(EnvEndpointURL, DefaultEndpointURL),
		Region:         stringEnv(EnvRegion, DefaultRegion),
		AccountID:      stringEnv(EnvAccountID, DefaultAccountID),
		Dir:            stringEnv(EnvDir, DefaultDir),
		KeyFile:        stringEnv(EnvKeyFile, DefaultKeyFile),
		Interval:       time.Duration(interval) * time.Second,
		Keep:           keep,
		Force:          os.Getenv(EnvForce) == forceEnabledVal,
		LoopLock:       DefaultLoopLock,
		RestoredMarker: DefaultRestoredMarker,
		ProcStat:       DefaultProcStat,
	}, nil
}

func stringEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func positiveIntEnv(name string, fallback, minimum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return 0, fmt.Errorf("%s must be an integer >= %d", name, minimum)
	}
	return value, nil
}
