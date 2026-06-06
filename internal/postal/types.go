package postal

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type WebhookEvent struct {
	Event     string          `json:"event"`
	UUID      string          `json:"uuid"`
	ID        string          `json:"id"`
	MessageID string          `json:"message_id"`
	Token     string          `json:"token"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
	Message   MessagePayload  `json:"message"`
	Status    string          `json:"status"`
	Details   string          `json:"details"`
	URL       string          `json:"url"`
}

type MessagePayload struct {
	ID        string
	MessageID string
	Token     string
	To        []string
	Recipient string
}

type messagePayloadWire struct {
	ID        flexibleString `json:"id"`
	MessageID flexibleString `json:"message_id"`
	Token     flexibleString `json:"token"`
	To        flexibleList   `json:"to"`
	Recipient flexibleString `json:"recipient"`
}

func (m *MessagePayload) UnmarshalJSON(data []byte) error {
	var wire messagePayloadWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*m = MessagePayload{
		ID:        string(wire.ID),
		MessageID: string(wire.MessageID),
		Token:     string(wire.Token),
		To:        []string(wire.To),
		Recipient: string(wire.Recipient),
	}
	return nil
}

type flexibleString string

func (s *flexibleString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = flexibleString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		*s = flexibleString(number.String())
		return nil
	}
	if string(data) == "null" {
		*s = ""
		return nil
	}
	return fmt.Errorf("value must be a string or number")
}

type flexibleList []string

func (l *flexibleList) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*l = splitRecipients(text)
		return nil
	}
	var values []flexibleString
	if err := json.Unmarshal(data, &values); err == nil {
		items := make([]string, 0, len(values))
		for _, value := range values {
			items = append(items, string(value))
		}
		*l = items
		return nil
	}
	if string(data) == "null" {
		*l = nil
		return nil
	}
	return fmt.Errorf("value must be a string or list")
}

func splitRecipients(value string) []string {
	parts := strings.Split(value, ",")
	recipients := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			recipients = append(recipients, trimmed)
		}
	}
	return recipients
}

func FlexibleString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	}
	return ""
}
