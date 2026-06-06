package sendgrid

import "encoding/json"

type ErrorResponse struct {
	Errors []ErrorItem `json:"errors"`
}

type ErrorItem struct {
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

type DomainRequest struct {
	Domain            string `json:"domain"`
	Subdomain         string `json:"subdomain"`
	AutomaticSecurity bool   `json:"automatic_security"`
	Default           bool   `json:"default"`
}

type DNSRecord struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Data  string `json:"data"`
	Valid bool   `json:"valid"`
}

type DomainResponse struct {
	ID        int64                `json:"id"`
	Domain    string               `json:"domain"`
	Subdomain string               `json:"subdomain"`
	Valid     bool                 `json:"valid"`
	DNS       map[string]DNSRecord `json:"dns"`
}

type ValidationRecord struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

type ValidateResponse struct {
	Valid             bool                        `json:"valid"`
	ValidationResults map[string]ValidationRecord `json:"validation_results"`
}

type MailAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type Attachment struct {
	Content     string `json:"content"`
	Filename    string `json:"filename"`
	Type        string `json:"type,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
	ContentId   string `json:"contentId,omitempty"`
}

type MailSendRequest struct {
	From                 MailAddress
	To                   []MailAddress
	Subject              string
	HTML                 string
	Text                 string
	ReplyTo              *MailAddress
	Headers              map[string]string
	Attachments          []Attachment
	CustomArgs           map[string]string
	TrackingOpenEnabled  bool
	TrackingClickEnabled bool
}

type mailSendWire struct {
	From             MailAddress       `json:"from"`
	To               []MailAddress     `json:"to"`
	Subject          string            `json:"subject"`
	HTML             string            `json:"html"`
	Text             string            `json:"text"`
	ReplyTo          *MailAddress      `json:"reply_to"`
	ReplyToCamel     *MailAddress      `json:"replyTo"`
	Headers          map[string]string `json:"headers"`
	Attachments      []Attachment      `json:"attachments"`
	CustomArgs       map[string]string `json:"custom_args"`
	CustomArgsCamel  map[string]string `json:"customArgs"`
	Personalizations []personalization `json:"personalizations"`
	TrackingSettings trackingSettings  `json:"tracking_settings"`
	TrackingCamel    trackingSettings  `json:"trackingSettings"`
}

type personalization struct {
	To              []MailAddress     `json:"to"`
	CC              []MailAddress     `json:"cc"`
	BCC             []MailAddress     `json:"bcc"`
	Subject         string            `json:"subject"`
	Headers         map[string]string `json:"headers"`
	CustomArgs      map[string]string `json:"custom_args"`
	CustomArgsCamel map[string]string `json:"customArgs"`
}

type trackingSettings struct {
	ClickTracking trackingClickSettings `json:"click_tracking"`
	ClickCamel    trackingClickSettings `json:"clickTracking"`
	OpenTracking  trackingOpenSettings  `json:"open_tracking"`
	OpenCamel     trackingOpenSettings  `json:"openTracking"`
}

type trackingClickSettings struct {
	Enable          *bool `json:"enable"`
	EnableText      *bool `json:"enable_text"`
	EnableTextCamel *bool `json:"enableText"`
}

type trackingOpenSettings struct {
	Enable *bool `json:"enable"`
}

type Event struct {
	Event       string            `json:"event"`
	SGMessageID string            `json:"sg_message_id"`
	Email       string            `json:"email"`
	Timestamp   int64             `json:"timestamp"`
	URL         string            `json:"url,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	CustomArgs  map[string]string `json:"custom_args,omitempty"`
}

func (r *MailSendRequest) UnmarshalJSON(data []byte) error {
	var wire mailSendWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = normalizeMailSend(wire)
	return nil
}

func normalizeMailSend(wire mailSendWire) MailSendRequest {
	request := MailSendRequest{
		From:                 wire.From,
		To:                   wire.To,
		Subject:              wire.Subject,
		HTML:                 wire.HTML,
		Text:                 wire.Text,
		ReplyTo:              firstAddressPointer(wire.ReplyTo, wire.ReplyToCamel),
		Headers:              mergeStringMaps(wire.Headers),
		Attachments:          wire.Attachments,
		CustomArgs:           mergeStringMaps(firstMap(wire.CustomArgs, wire.CustomArgsCamel)),
		TrackingOpenEnabled:  openTrackingEnabled(wire),
		TrackingClickEnabled: clickTrackingEnabled(wire),
	}
	if len(wire.Personalizations) == 0 {
		return request
	}
	personalization := wire.Personalizations[0]
	request.To = append(append([]MailAddress{}, personalization.To...), append(personalization.CC, personalization.BCC...)...)
	request.Headers = mergeStringMaps(request.Headers, personalization.Headers)
	request.CustomArgs = mergeStringMaps(request.CustomArgs, firstMap(personalization.CustomArgs, personalization.CustomArgsCamel))
	if personalization.Subject != "" {
		request.Subject = personalization.Subject
	}
	return request
}

func firstAddressPointer(values ...*MailAddress) *MailAddress {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func mergeStringMaps(values ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, value := range values {
		for key, item := range value {
			merged[key] = item
		}
	}
	return merged
}

func openTrackingEnabled(wire mailSendWire) bool {
	for _, value := range []*bool{
		wire.TrackingSettings.OpenTracking.Enable,
		wire.TrackingSettings.OpenCamel.Enable,
		wire.TrackingCamel.OpenTracking.Enable,
		wire.TrackingCamel.OpenCamel.Enable,
	} {
		if value != nil {
			return *value
		}
	}
	return true
}

func clickTrackingEnabled(wire mailSendWire) bool {
	for _, value := range []*bool{
		wire.TrackingSettings.ClickTracking.Enable,
		wire.TrackingSettings.ClickCamel.Enable,
		wire.TrackingCamel.ClickTracking.Enable,
		wire.TrackingCamel.ClickCamel.Enable,
	} {
		if value != nil {
			return *value
		}
	}
	return true
}
