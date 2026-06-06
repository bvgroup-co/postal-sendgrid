package postal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type Address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Data        string `json:"data"`
}

type SendMessageRequest struct {
	To          []string          `json:"to"`
	From        string            `json:"from"`
	Sender      string            `json:"sender,omitempty"`
	Subject     string            `json:"subject"`
	HTMLBody    string            `json:"html_body,omitempty"`
	PlainBody   string            `json:"plain_body,omitempty"`
	ReplyTo     string            `json:"reply_to,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Attachments []Attachment      `json:"attachments,omitempty"`
}

type SendMessageResponse struct {
	MessageID string `json:"message_id"`
	ID        string `json:"id"`
	Token     string `json:"token"`
	Status    string `json:"status"`
	Data      struct {
		ID        string `json:"id"`
		MessageID string `json:"message_id"`
		Token     string `json:"token"`
	} `json:"data"`
}

func NewClient(baseURL string, apiKey string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, httpClient: httpClient}
}

func (c *Client) SendMessage(ctx context.Context, request SendMessageRequest) (SendMessageResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return SendMessageResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/send/message", bytes.NewReader(body))
	if err != nil {
		return SendMessageResponse{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Server-API-Key", c.apiKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return SendMessageResponse{}, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SendMessageResponse{}, HTTPError{StatusCode: response.StatusCode}
	}

	var parsed SendMessageResponse
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		return SendMessageResponse{}, err
	}
	return parsed, nil
}

type HTTPError struct {
	StatusCode int
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("Postal returned HTTP %d", e.StatusCode)
}
