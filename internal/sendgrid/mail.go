package sendgrid

func (r MailSendRequest) CustomArguments() map[string]string {
	return r.CustomArgs
}

func (r MailSendRequest) ReplyAddress() *MailAddress {
	return r.ReplyTo
}

func (r MailSendRequest) OpenTrackingEnabled() bool {
	return r.TrackingOpenEnabled
}

func (r MailSendRequest) ClickTrackingEnabled() bool {
	return r.TrackingClickEnabled
}
