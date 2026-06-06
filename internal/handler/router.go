package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bvgroup-co/postal-sendgrid/internal/domain"
	"github.com/bvgroup-co/postal-sendgrid/internal/postal"
	"github.com/bvgroup-co/postal-sendgrid/internal/sendgrid"
	"github.com/bvgroup-co/postal-sendgrid/internal/storage"
	"github.com/bvgroup-co/postal-sendgrid/internal/webhook"
)

type PostalSender interface {
	SendMessage(context.Context, postal.SendMessageRequest) (postal.SendMessageResponse, error)
}

type Router struct {
	authToken       string
	mailMaxBytes    int64
	webhookMaxBytes int64
	domains         *domain.Service
	postal          PostalSender
	webhooks        *webhook.Forwarder
	store           *storage.Store
}

func NewRouter(authToken string, mailMaxBytes int64, webhookMaxBytes int64, domains *domain.Service, postal PostalSender, webhooks *webhook.Forwarder, store *storage.Store) http.Handler {
	router := &Router{authToken: authToken, mailMaxBytes: mailMaxBytes, webhookMaxBytes: webhookMaxBytes, domains: domains, postal: postal, webhooks: webhooks, store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", router.health)
	mux.HandleFunc("POST /v3/whitelabel/domains", router.requireAuth(router.createDomain))
	mux.HandleFunc("POST /v3/whitelabel/domains/{id}/validate", router.requireAuth(router.validateDomain))
	mux.HandleFunc("DELETE /v3/whitelabel/domains/{id}", router.requireAuth(router.deleteDomain))
	mux.HandleFunc("POST /v3/mail/send", router.requireAuth(router.sendMail))
	mux.HandleFunc("POST /webhooks/postal", router.postalWebhook)
	return mux
}

func (r *Router) health(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			WriteError(w, APIError{Status: http.StatusUnauthorized, Message: "Missing bearer token"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(r.authToken)) != 1 {
			WriteError(w, APIError{Status: http.StatusForbidden, Message: "Invalid bearer token"})
			return
		}
		next(w, request)
	}
}

func (r *Router) createDomain(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, 1<<20)
	var payload sendgrid.DomainRequest
	if err := DecodeJSON(request, &payload); err != nil {
		WriteError(w, err)
		return
	}
	if strings.TrimSpace(payload.Domain) == "" {
		WriteError(w, APIError{Status: http.StatusBadRequest, Message: "Domain is required", Field: "domain"})
		return
	}
	response, err := r.domains.Create(request.Context(), payload)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateDomain) {
			WriteError(w, APIError{Status: http.StatusConflict, Message: "Domain already exists", Field: "domain"})
			return
		}
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, response)
}

func (r *Router) validateDomain(w http.ResponseWriter, request *http.Request) {
	id, err := parseID(request.PathValue("id"))
	if err != nil {
		WriteError(w, err)
		return
	}
	response, err := r.domains.Validate(request.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, response)
}

func (r *Router) deleteDomain(w http.ResponseWriter, request *http.Request) {
	id, err := parseID(request.PathValue("id"))
	if err != nil {
		WriteError(w, err)
		return
	}
	if err := r.domains.Delete(request.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	WriteNoContent(w)
}

func (r *Router) sendMail(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, r.mailMaxBytes)
	var payload sendgrid.MailSendRequest
	if err := DecodeJSON(request, &payload); err != nil {
		WriteError(w, err)
		return
	}
	if err := validateMail(payload); err != nil {
		WriteError(w, err)
		return
	}

	shimMessageID := webhook.NewMessageID()
	postalRequest := mapPostalRequest(payload, shimMessageID)
	postalResponse, err := r.postal.SendMessage(request.Context(), postalRequest)
	if err != nil {
		var httpErr postal.HTTPError
		if errors.As(err, &httpErr) {
			WriteError(w, APIError{Status: http.StatusBadGateway, Message: "Postal request failed"})
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			WriteError(w, APIError{Status: http.StatusGatewayTimeout, Message: "Postal request timed out"})
			return
		}
		WriteError(w, APIError{Status: http.StatusBadGateway, Message: "Postal unavailable"})
		return
	}

	customArgs := payload.CustomArguments()
	customArgsJSON, err := json.Marshal(customArgs)
	if err != nil {
		WriteError(w, err)
		return
	}
	mapping := storage.MessageMapping{
		ShimMessageID:        shimMessageID,
		PostalMessageID:      firstNonEmpty(postalResponse.MessageID, postalResponse.ID, postalResponse.Data.MessageID, postalResponse.Data.ID),
		PostalMessageToken:   firstNonEmpty(postalResponse.Token, postalResponse.Data.Token),
		PlunkEmailID:         customArgs["plunk_email_id"],
		PlunkProjectID:       customArgs["plunk_project_id"],
		Recipient:            payload.To[0].Email,
		Sender:               payload.From.Email,
		Subject:              payload.Subject,
		CustomArgsJSON:       string(customArgsJSON),
		TrackingOpenEnabled:  payload.OpenTrackingEnabled(),
		TrackingClickEnabled: payload.ClickTrackingEnabled(),
	}
	if err := r.store.SaveMessageMapping(request.Context(), mapping); err != nil {
		WriteError(w, err)
		return
	}

	w.Header().Set("x-message-id", shimMessageID)
	w.WriteHeader(http.StatusAccepted)
}

func (r *Router) postalWebhook(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, r.webhookMaxBytes)
	var event postal.WebhookEvent
	if err := DecodeJSON(request, &event); err != nil {
		WriteError(w, err)
		return
	}
	result, err := r.webhooks.Handle(request.Context(), event)
	if err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, result)
}

func validateMail(payload sendgrid.MailSendRequest) error {
	if payload.From.Email == "" {
		return APIError{Status: http.StatusBadRequest, Message: "From email is required", Field: "from.email"}
	}
	if len(payload.To) == 0 || payload.To[0].Email == "" {
		return APIError{Status: http.StatusBadRequest, Message: "At least one recipient is required", Field: "to"}
	}
	if payload.Subject == "" {
		return APIError{Status: http.StatusBadRequest, Message: "Subject is required", Field: "subject"}
	}
	if payload.HTML == "" && payload.Text == "" {
		return APIError{Status: http.StatusBadRequest, Message: "HTML or text body is required"}
	}
	return nil
}

func mapPostalRequest(payload sendgrid.MailSendRequest, shimMessageID string) postal.SendMessageRequest {
	headers := make(map[string]string, len(payload.Headers)+1)
	for key, value := range payload.Headers {
		headers[key] = value
	}
	headers["X-Shim-Message-ID"] = shimMessageID

	attachments := make([]postal.Attachment, 0, len(payload.Attachments))
	for _, attachment := range payload.Attachments {
		attachments = append(attachments, postal.Attachment{Name: attachment.Filename, ContentType: attachment.Type, Data: attachment.Content})
	}

	request := postal.SendMessageRequest{
		To:          emailList(payload.To),
		From:        formatAddress(payload.From),
		Subject:     payload.Subject,
		HTMLBody:    payload.HTML,
		PlainBody:   payload.Text,
		Headers:     headers,
		Attachments: attachments,
	}
	if reply := payload.ReplyAddress(); reply != nil {
		request.ReplyTo = formatAddress(*reply)
	}
	return request
}

func emailList(addresses []sendgrid.MailAddress) []string {
	emails := make([]string, 0, len(addresses))
	for _, address := range addresses {
		emails = append(emails, formatAddress(address))
	}
	return emails
}

func formatAddress(address sendgrid.MailAddress) string {
	if address.Name == "" {
		return address.Email
	}
	return address.Name + " <" + address.Email + ">"
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, APIError{Status: http.StatusBadRequest, Message: "Domain ID must be a positive integer", Field: "id"}
	}
	return id, nil
}

func writeDomainError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrDomainNotFound) {
		WriteError(w, APIError{Status: http.StatusNotFound, Message: "Domain not found"})
		return
	}
	WriteError(w, err)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
