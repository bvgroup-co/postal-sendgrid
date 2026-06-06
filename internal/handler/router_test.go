package handler

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bvgroup-co/postal-sendgrid/internal/domain"
	"github.com/bvgroup-co/postal-sendgrid/internal/postal"
	"github.com/bvgroup-co/postal-sendgrid/internal/sendgrid"
	"github.com/bvgroup-co/postal-sendgrid/internal/storage"
	"github.com/bvgroup-co/postal-sendgrid/internal/webhook"
)

const testSigningPrivateKeyPEM = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIEgi3TqYKxxy1SS9cZf/HRW0eWzf97vZ5xAwtkwpLd+koAoGCCqGSM49
AwEHoUQDQgAEk9OjifEw69U7XPhMsiZU1GH3hk3exFYsW1o7AARFT9I4qL07AU+r
hZ+6jwERjQ2ehizcP4szwWmZxFifA6C7sQ==
-----END EC PRIVATE KEY-----`

type fakePostal struct {
	request postal.SendMessageRequest
}

func (f *fakePostal) SendMessage(_ context.Context, request postal.SendMessageRequest) (postal.SendMessageResponse, error) {
	f.request = request
	return postal.SendMessageResponse{MessageID: "postal-message-id", Token: "postal-token"}, nil
}

func TestDomainLifecycle(t *testing.T) {
	server, _, cleanup := testServer(t, nil)
	defer cleanup()

	createResponse := doJSON(t, server, http.MethodPost, "/v3/whitelabel/domains", sendgrid.DomainRequest{Domain: "example.com", Subdomain: "mail"})
	if createResponse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", createResponse.Code, createResponse.Body.String())
	}
	var domainResponse sendgrid.DomainResponse
	decodeResponse(t, createResponse, &domainResponse)
	if domainResponse.ID == 0 || domainResponse.DNS["mail_cname"].Host != "mail.example.com" {
		t.Fatalf("unexpected domain response: %#v", domainResponse)
	}

	validateResponse := doJSON(t, server, http.MethodPost, "/v3/whitelabel/domains/"+itoa(domainResponse.ID)+"/validate", map[string]string{})
	if validateResponse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", validateResponse.Code, validateResponse.Body.String())
	}
	var validation sendgrid.ValidateResponse
	decodeResponse(t, validateResponse, &validation)
	if validation.Valid || validation.ValidationResults["mail_cname"].Reason == "" {
		t.Fatalf("unexpected validation response: %#v", validation)
	}

	deleteResponse := doJSON(t, server, http.MethodDelete, "/v3/whitelabel/domains/"+itoa(domainResponse.ID), nil)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestSendMailMapsPlunkProviderJSONAndStoresMapping(t *testing.T) {
	postalClient := &fakePostal{}
	server, store, cleanup := testServer(t, postalClient)
	defer cleanup()

	response := doJSON(t, server, http.MethodPost, "/v3/mail/send", realPlunkProviderPayload())
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	shimMessageID := response.Header().Get("x-message-id")
	if shimMessageID == "" {
		t.Fatal("missing x-message-id")
	}
	if postalClient.request.From != "\"Sender\" <sender@example.com>" || postalClient.request.To[0] != "\"Recipient\" <recipient@example.net>" {
		t.Fatalf("unexpected Postal request: %#v", postalClient.request)
	}
	if postalClient.request.Headers["X-Shim-Message-ID"] != shimMessageID || postalClient.request.Headers["List-Unsubscribe"] == "" {
		t.Fatalf("missing mapped headers: %#v", postalClient.request.Headers)
	}

	mapping, found, err := store.FindMessageMapping(context.Background(), shimMessageID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found || mapping.PostalMessageID != "postal-message-id" || mapping.PlunkEmailID != "email_123" || mapping.RecipientsJSON != `["recipient@example.net"]` || mapping.TrackingOpenEnabled {
		t.Fatalf("unexpected mapping: %#v", mapping)
	}
}

func TestSendMailRejectsMalformedAddress(t *testing.T) {
	server, _, cleanup := testServer(t, &fakePostal{})
	defer cleanup()

	payload := realPlunkProviderPayload()
	payload["from"] = map[string]string{"email": "sender@example.com\r\nBcc: injected@example.com", "name": "Sender"}
	response := doJSON(t, server, http.MethodPost, "/v3/mail/send", payload)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestWebhookUsesMatchingRecipientAndStableEventID(t *testing.T) {
	var forwarded [][]sendgrid.Event
	plunk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body := readBody(t, request)
		var events []sendgrid.Event
		if err := json.Unmarshal(body, &events); err != nil {
			t.Fatal(err)
		}
		forwarded = append(forwarded, events)
		w.WriteHeader(http.StatusOK)
	}))
	defer plunk.Close()

	postalClient := &fakePostal{}
	server, _, cleanup := testServerWithPlunk(t, postalClient, plunk.URL)
	defer cleanup()

	payload := realPlunkProviderPayload()
	payload["personalizations"] = []map[string]any{{
		"to": []map[string]string{
			{"email": "first@example.net", "name": "First"},
			{"email": "second@example.net", "name": "Second"},
		},
	}}
	response := doJSON(t, server, http.MethodPost, "/v3/mail/send", payload)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}

	postalEvent := map[string]any{
		"event":     "MessageLoaded",
		"uuid":      "event-second-recipient",
		"timestamp": 1760000000,
		"message": map[string]any{
			"message_id": "postal-message-id",
			"to":         "second@example.net",
		},
	}
	webhookResponse := doJSON(t, server, http.MethodPost, "/webhooks/postal", postalEvent)
	if webhookResponse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", webhookResponse.Code, webhookResponse.Body.String())
	}
	if len(forwarded) != 1 || len(forwarded[0]) != 1 {
		t.Fatalf("expected one forwarded event, got %#v", forwarded)
	}
	event := forwarded[0][0]
	if event.Email != "second@example.net" || event.SGEventID != "event-second-recipient" {
		t.Fatalf("unexpected forwarded event: %#v", event)
	}
}

func TestSendMailMapsPersonalizationPayload(t *testing.T) {
	postalClient := &fakePostal{}
	server, store, cleanup := testServer(t, postalClient)
	defer cleanup()

	response := doJSON(t, server, http.MethodPost, "/v3/mail/send", map[string]any{
		"from":    map[string]string{"email": "sender@example.com"},
		"subject": "Default subject",
		"html":    "<p>Hello</p>",
		"personalizations": []map[string]any{{
			"to":      []map[string]string{{"email": "recipient@example.net"}},
			"subject": "Personal subject",
			"headers": map[string]string{"X-Personal": "value"},
			"custom_args": map[string]string{
				"plunk_email_id":   "email_personalized",
				"plunk_project_id": "project_personalized",
			},
		}},
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	if postalClient.request.Subject != "Personal subject" || postalClient.request.Headers["X-Personal"] != "value" {
		t.Fatalf("unexpected Postal personalization mapping: %#v", postalClient.request)
	}
	mapping, found, err := store.FindMessageMapping(context.Background(), response.Header().Get("x-message-id"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found || mapping.PlunkEmailID != "email_personalized" {
		t.Fatalf("unexpected mapping: %#v", mapping)
	}
}

func TestPostalWebhookForwardsSignedSendGridEventAndDeduplicates(t *testing.T) {
	var forwarded [][]sendgrid.Event
	plunk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/webhooks/sendgrid/events" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		body := readBody(t, request)
		assertValidSignature(t, request, body)
		var events []sendgrid.Event
		if err := json.Unmarshal(body, &events); err != nil {
			t.Fatal(err)
		}
		forwarded = append(forwarded, events)
		w.WriteHeader(http.StatusOK)
	}))
	defer plunk.Close()

	postalClient := &fakePostal{}
	server, _, cleanup := testServerWithPlunk(t, postalClient, plunk.URL)
	defer cleanup()

	sendResponse := doJSON(t, server, http.MethodPost, "/v3/mail/send", realPlunkProviderPayload())
	if sendResponse.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", sendResponse.Code)
	}

	postalEvent := map[string]any{
		"event":     "MessageLoaded",
		"uuid":      "event-1",
		"timestamp": 1760000000,
		"url":       "https://example.net",
		"message": map[string]any{
			"id":         12345,
			"message_id": "postal-message-id",
			"to":         "recipient@example.net",
		},
	}
	webhookResponse := doJSON(t, server, http.MethodPost, "/webhooks/postal", postalEvent)
	if webhookResponse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", webhookResponse.Code, webhookResponse.Body.String())
	}
	duplicateResponse := doJSON(t, server, http.MethodPost, "/webhooks/postal", postalEvent)
	if duplicateResponse.Code != http.StatusOK {
		t.Fatalf("expected duplicate 200, got %d", duplicateResponse.Code)
	}
	if len(forwarded) != 1 {
		t.Fatalf("expected one forwarded payload, got %d", len(forwarded))
	}
	event := forwarded[0][0]
	if event.Event != "open" || event.SGMessageID != sendResponse.Header().Get("x-message-id") || event.SGEventID != "event-1" || event.CustomArgs["plunk_email_id"] != "email_123" {
		t.Fatalf("unexpected forwarded event: %#v", event)
	}
}

func realPlunkProviderPayload() map[string]any {
	return map[string]any{
		"from":    map[string]string{"email": "sender@example.com", "name": "Sender"},
		"to":      []map[string]string{{"email": "recipient@example.net", "name": "Recipient"}},
		"subject": "Hello",
		"html":    "<p>Hello</p>",
		"replyTo": map[string]string{"email": "reply@example.com"},
		"headers": map[string]string{
			"X-Custom":         "value",
			"List-Unsubscribe": "<https://example.com/unsubscribe>",
		},
		"attachments": []map[string]string{{"content": "SGVsbG8=", "filename": "file.txt", "type": "text/plain", "disposition": "attachment"}},
		"customArgs": map[string]string{
			"plunk_email_id":   "email_123",
			"plunk_project_id": "project_123",
		},
		"mailSettings": map[string]any{"sandboxMode": map[string]bool{"enable": false}},
		"trackingSettings": map[string]any{
			"clickTracking": map[string]bool{"enable": false, "enableText": false},
			"openTracking":  map[string]bool{"enable": false},
		},
	}
}

func testServer(t *testing.T, postalClient *fakePostal) (http.Handler, *storage.Store, func()) {
	return testServerWithPlunk(t, postalClient, "http://plunk.invalid")
}

func testServerWithPlunk(t *testing.T, postalClient *fakePostal, plunkURL string) (http.Handler, *storage.Store, func()) {
	t.Helper()
	if postalClient == nil {
		postalClient = &fakePostal{}
	}
	store, err := storage.Open(filepath.Join(t.TempDir(), "shim.db"))
	if err != nil {
		t.Fatal(err)
	}
	domainService := domain.NewService(store, "postal.example.com", false)
	forwarder := webhook.NewForwarder(store, plunkURL, http.DefaultClient, 1, time.Millisecond, true, testSigningPrivateKey(t))
	server := NewRouter("test-token", 15*1024*1024, 1024*1024, domainService, postalClient, forwarder, store)
	return server, store, func() { _ = store.Close() }
}

func doJSON(t *testing.T, handler http.Handler, method string, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, request *http.Request) []byte {
	t.Helper()
	var body bytes.Buffer
	if _, err := body.ReadFrom(request.Body); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func assertValidSignature(t *testing.T, request *http.Request, body []byte) {
	t.Helper()
	timestamp := request.Header.Get("X-Twilio-Email-Event-Webhook-Timestamp")
	signature := request.Header.Get("X-Twilio-Email-Event-Webhook-Signature")
	if timestamp == "" || signature == "" {
		t.Fatalf("missing signature headers: %#v", request.Header)
	}
	rawSignature, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte(timestamp), body...))
	if !ecdsa.VerifyASN1(&testSigningPrivateKey(t).PublicKey, digest[:], rawSignature) {
		t.Fatal("signature did not verify against public key")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&testSigningPrivateKey(t).PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	parsedPublic, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, ok := parsedPublic.(*ecdsa.PublicKey)
	if !ok || !ecdsa.VerifyASN1(publicKey, digest[:], rawSignature) {
		t.Fatal("signature did not verify using Plunk-style public key parsing")
	}
}

func testSigningPrivateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode([]byte(testSigningPrivateKeyPEM))
	if block == nil {
		t.Fatal("test private key PEM did not decode")
	}
	privateKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
