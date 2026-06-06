package webhook

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bvgroup-co/postal-sendgrid/internal/postal"
	"github.com/bvgroup-co/postal-sendgrid/internal/sendgrid"
	"github.com/bvgroup-co/postal-sendgrid/internal/storage"
)

type Forwarder struct {
	store          *storage.Store
	endpoint       string
	httpClient     *http.Client
	attempts       int
	backoff        time.Duration
	sleep          func(time.Duration)
	signingEnabled bool
	signingKey     *ecdsa.PrivateKey
}

type Result struct {
	Success   bool   `json:"success"`
	Forwarded bool   `json:"forwarded"`
	Duplicate bool   `json:"duplicate"`
	Ignored   bool   `json:"ignored"`
	Event     string `json:"event,omitempty"`
}

func NewForwarder(store *storage.Store, plunkBaseURL string, httpClient *http.Client, attempts int, backoff time.Duration, signingEnabled bool, signingKey *ecdsa.PrivateKey) *Forwarder {
	return &Forwarder{
		store:          store,
		endpoint:       strings.TrimRight(plunkBaseURL, "/") + "/webhooks/sendgrid/events",
		httpClient:     httpClient,
		attempts:       attempts,
		backoff:        backoff,
		sleep:          time.Sleep,
		signingEnabled: signingEnabled,
		signingKey:     signingKey,
	}
}

func NewMessageID() string {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		panic(fmt.Sprintf("failed to generate message ID: %v", err))
	}
	return "shim-" + hex.EncodeToString(random)
}

func ParseSigningPrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block != nil {
		return parseSigningPrivateKeyDER(block.Bytes)
	}
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return parseSigningPrivateKeyDER(der)
}

func SigningPublicKeyDER(privateKey *ecdsa.PrivateKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
}

func (f *Forwarder) Handle(ctx context.Context, event postal.WebhookEvent) (Result, error) {
	mappedEvent, ok := mapPostalEvent(event.Event, event.Status)
	if !ok {
		return Result{Success: true, Ignored: true}, nil
	}

	mapping, found, err := f.store.FindMessageMapping(ctx, event.MessageID, firstNonEmpty(event.Message.MessageID, event.Message.ID, event.ID), firstNonEmpty(event.Message.Token, event.Token))
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, errors.New("could not correlate Postal event to a shim message")
	}

	providerEventID := providerEventID(event, mapping.ShimMessageID)
	recipient, err := eventRecipient(event, mapping)
	if err != nil {
		return Result{}, err
	}
	sendGridEvent := sendgrid.Event{
		Event:       mappedEvent,
		SGEventID:   providerEventID,
		SGMessageID: mapping.ShimMessageID,
		Email:       recipient,
		Timestamp:   eventTimestamp(event.Timestamp),
		URL:         event.URL,
		Reason:      firstNonEmpty(event.Details, event.Status),
		CustomArgs:  customArgs(mapping.CustomArgsJSON),
	}
	payload := []sendgrid.Event{sendGridEvent}

	forwarded, err := f.store.HasForwardedEvent(ctx, providerEventID)
	if err != nil {
		return Result{}, err
	}
	if forwarded {
		return Result{Success: true, Duplicate: true, Event: mappedEvent}, nil
	}

	if err := f.forward(ctx, payload); err != nil {
		return Result{}, err
	}
	if err := f.store.RecordForwardedEvent(ctx, providerEventID, mapping.ShimMessageID, event.Event, payload); err != nil {
		if errors.Is(err, storage.ErrEventDuplicate) {
			return Result{Success: true, Duplicate: true, Event: mappedEvent}, nil
		}
		return Result{}, err
	}
	return Result{Success: true, Forwarded: true, Event: mappedEvent}, nil
}

func (f *Forwarder) forward(ctx context.Context, payload []sendgrid.Event) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= f.attempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		if err := f.sign(request, body); err != nil {
			return err
		}
		response, err := f.httpClient.Do(request)
		if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			_ = response.Body.Close()
			return nil
		}
		if err == nil {
			lastErr = fmt.Errorf("Plunk webhook returned HTTP %d", response.StatusCode)
			_ = response.Body.Close()
			if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
				return lastErr
			}
		} else {
			lastErr = err
		}
		if attempt < f.attempts {
			f.sleep(f.backoff * time.Duration(1<<(attempt-1)))
		}
	}
	return lastErr
}

func (f *Forwarder) sign(request *http.Request, body []byte) error {
	if !f.signingEnabled {
		return nil
	}
	if f.signingKey == nil {
		panic("webhook signing enabled without a private key")
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(append([]byte(timestamp), body...))
	signature, err := ecdsa.SignASN1(rand.Reader, f.signingKey, digest[:])
	if err != nil {
		return err
	}
	request.Header.Set("X-Twilio-Email-Event-Webhook-Timestamp", timestamp)
	request.Header.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString(signature))
	return nil
}

func parseSigningPrivateKeyDER(der []byte) (*ecdsa.PrivateKey, error) {
	privateKey, err := x509.ParseECPrivateKey(der)
	if err == nil {
		if privateKey.Curve != elliptic.P256() {
			return nil, fmt.Errorf("webhook signing private key must use P-256")
		}
		return privateKey, nil
	}
	parsedKey, parseErr := x509.ParsePKCS8PrivateKey(der)
	if parseErr != nil {
		return nil, err
	}
	privateKey, ok := parsedKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("webhook signing private key must be an ECDSA key")
	}
	if privateKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("webhook signing private key must use P-256")
	}
	return privateKey, nil
}

func mapPostalEvent(event string, status string) (string, bool) {
	normalized := strings.ToLower(event)
	switch normalized {
	case "messagesent", "messagesentevent", "messagedelivered", "delivered", "sent":
		return "delivered", true
	case "messagebounced", "bounced", "bounce":
		return "bounce", true
	case "messagefailed", "messagedeliveryfailed", "failed", "held":
		return failureEvent(status), true
	case "messagelinkclicked", "clicked", "click":
		return "click", true
	case "messageloaded", "loaded", "open", "opened":
		return "open", true
	}
	return "", false
}

func failureEvent(status string) string {
	switch strings.ToLower(status) {
	case "hardfail", "hard_fail", "bounce", "bounced":
		return "bounce"
	}
	return "dropped"
}

func providerEventID(event postal.WebhookEvent, shimMessageID string) string {
	if event.UUID != "" {
		return event.UUID
	}
	if event.ID != "" && event.Event != "" {
		return event.Event + ":" + event.ID
	}
	data, err := json.Marshal(event)
	if err != nil {
		panic(fmt.Sprintf("failed to serialize Postal event: %v", err))
	}
	digest := sha256.Sum256(append([]byte(shimMessageID), data...))
	return hex.EncodeToString(digest[:])
}

func customArgs(raw string) map[string]string {
	values := map[string]string{}
	if raw == "" {
		return values
	}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		panic(fmt.Sprintf("stored custom args are invalid: %v", err))
	}
	return values
}

func eventTimestamp(timestamp int64) int64 {
	if timestamp > 0 {
		return timestamp
	}
	return time.Now().Unix()
}

func eventRecipient(event postal.WebhookEvent, mapping storage.MessageMapping) (string, error) {
	postalRecipient := firstNonEmpty(event.Message.Recipient, firstMessageRecipient(event.Message.To))
	if postalRecipient == "" {
		return mapping.Recipient, nil
	}
	for _, recipient := range mappingRecipients(mapping) {
		if strings.EqualFold(postalRecipient, recipient) {
			return recipient, nil
		}
	}
	return "", fmt.Errorf("Postal event recipient %q is not in stored recipient mapping", postalRecipient)
}

func mappingRecipients(mapping storage.MessageMapping) []string {
	var recipients []string
	if mapping.RecipientsJSON != "" {
		if err := json.Unmarshal([]byte(mapping.RecipientsJSON), &recipients); err != nil {
			panic(fmt.Sprintf("stored recipients are invalid: %v", err))
		}
	}
	if len(recipients) == 0 {
		panic("stored recipients are empty")
	}
	return recipients
}

func firstMessageRecipient(recipients []string) string {
	if len(recipients) == 0 {
		return ""
	}
	return recipients[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
