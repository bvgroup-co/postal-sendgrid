package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr            = ":8080"
	defaultDatabasePath          = "postal-sendgrid.db"
	defaultMailMaxBytes          = 15 * 1024 * 1024
	defaultWebhookMaxBytes       = 1 * 1024 * 1024
	defaultHTTPTimeout           = 10 * time.Second
	defaultForwardAttempts       = 4
	defaultForwardBackoff        = 250 * time.Millisecond
	defaultDNSCheckEnabled       = false
	defaultPostalCNAMEValue      = "postal.example.invalid"
	defaultWebhookSigningEnabled = true
)

type Config struct {
	ListenAddr               string
	AuthToken                string
	PostalBaseURL            string
	PostalAPIKey             string
	PlunkWebhookBaseURL      string
	DatabasePath             string
	MailMaxBytes             int64
	WebhookMaxBytes          int64
	HTTPTimeout              time.Duration
	ForwardAttempts          int
	ForwardBackoff           time.Duration
	DNSCheckEnabled          bool
	PostalCNAMEValue         string
	WebhookSigningEnabled    bool
	WebhookSigningPrivateKey *ecdsa.PrivateKey
}

func Load() (Config, error) {
	signingKey, err := parseSigningPrivateKey(os.Getenv("WEBHOOK_SIGNING_PRIVATE_KEY"))
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:               getEnv("LISTEN_ADDR", defaultListenAddr),
		AuthToken:                os.Getenv("SHIM_AUTH_TOKEN"),
		PostalBaseURL:            trimTrailingSlash(os.Getenv("POSTAL_BASE_URL")),
		PostalAPIKey:             os.Getenv("POSTAL_API_KEY"),
		PlunkWebhookBaseURL:      trimTrailingSlash(os.Getenv("PLUNK_WEBHOOK_BASE_URL")),
		DatabasePath:             getEnv("DATABASE_PATH", defaultDatabasePath),
		MailMaxBytes:             getEnvInt64("MAIL_MAX_BYTES", defaultMailMaxBytes),
		WebhookMaxBytes:          getEnvInt64("WEBHOOK_MAX_BYTES", defaultWebhookMaxBytes),
		HTTPTimeout:              getEnvDuration("HTTP_TIMEOUT", defaultHTTPTimeout),
		ForwardAttempts:          getEnvInt("FORWARD_ATTEMPTS", defaultForwardAttempts),
		ForwardBackoff:           getEnvDuration("FORWARD_BACKOFF", defaultForwardBackoff),
		DNSCheckEnabled:          getEnvBool("DNS_CHECK_ENABLED", defaultDNSCheckEnabled),
		PostalCNAMEValue:         getEnv("POSTAL_CNAME_VALUE", defaultPostalCNAMEValue),
		WebhookSigningEnabled:    getEnvBool("WEBHOOK_SIGNING_ENABLED", defaultWebhookSigningEnabled),
		WebhookSigningPrivateKey: signingKey,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	if c.AuthToken == "" {
		missing = append(missing, "SHIM_AUTH_TOKEN")
	}
	if c.PostalBaseURL == "" {
		missing = append(missing, "POSTAL_BASE_URL")
	}
	if c.PostalAPIKey == "" {
		missing = append(missing, "POSTAL_API_KEY")
	}
	if c.PlunkWebhookBaseURL == "" {
		missing = append(missing, "PLUNK_WEBHOOK_BASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if err := validateHTTPURL("POSTAL_BASE_URL", c.PostalBaseURL); err != nil {
		return err
	}
	if err := validateHTTPURL("PLUNK_WEBHOOK_BASE_URL", c.PlunkWebhookBaseURL); err != nil {
		return err
	}
	if c.MailMaxBytes <= 0 {
		return errors.New("MAIL_MAX_BYTES must be greater than zero")
	}
	if c.WebhookMaxBytes <= 0 {
		return errors.New("WEBHOOK_MAX_BYTES must be greater than zero")
	}
	if c.HTTPTimeout <= 0 {
		return errors.New("HTTP_TIMEOUT must be greater than zero")
	}
	if c.ForwardAttempts <= 0 {
		return errors.New("FORWARD_ATTEMPTS must be greater than zero")
	}
	if c.ForwardBackoff <= 0 {
		return errors.New("FORWARD_BACKOFF must be greater than zero")
	}
	if c.PostalCNAMEValue == "" {
		return errors.New("POSTAL_CNAME_VALUE must not be empty")
	}
	if c.WebhookSigningEnabled && c.WebhookSigningPrivateKey == nil {
		return errors.New("WEBHOOK_SIGNING_PRIVATE_KEY is required when WEBHOOK_SIGNING_ENABLED is true")
	}
	return nil
}

func validateHTTPURL(name string, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute http or https URL", name)
	}
	return nil
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getEnvInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil {
		return parsed
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func trimTrailingSlash(value string) string {
	return strings.TrimRight(value, "/")
}

func parseSigningPrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	if raw == "" {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(raw))
	if block != nil {
		return parseSigningPrivateKeyDER(block.Bytes)
	}
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("WEBHOOK_SIGNING_PRIVATE_KEY must be PEM or base64 DER: %w", err)
	}
	return parseSigningPrivateKeyDER(der)
}

func parseSigningPrivateKeyDER(der []byte) (*ecdsa.PrivateKey, error) {
	privateKey, err := x509.ParseECPrivateKey(der)
	if err == nil {
		return validateSigningPrivateKey(privateKey)
	}
	parsedKey, parseErr := x509.ParsePKCS8PrivateKey(der)
	if parseErr != nil {
		return nil, fmt.Errorf("WEBHOOK_SIGNING_PRIVATE_KEY must be an ECDSA private key: %w", err)
	}
	privateKey, ok := parsedKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("WEBHOOK_SIGNING_PRIVATE_KEY must be an ECDSA private key")
	}
	return validateSigningPrivateKey(privateKey)
}

func validateSigningPrivateKey(privateKey *ecdsa.PrivateKey) (*ecdsa.PrivateKey, error) {
	if privateKey.Curve != elliptic.P256() {
		return nil, errors.New("WEBHOOK_SIGNING_PRIVATE_KEY must use the P-256 curve")
	}
	return privateKey, nil
}
