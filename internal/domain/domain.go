package domain

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/bvgroup-co/postal-sendgrid/internal/sendgrid"
	"github.com/bvgroup-co/postal-sendgrid/internal/storage"
)

const mailCNAMEKey = "mail_cname"

type Service struct {
	store       *storage.Store
	postalCNAME string
	dnsCheck    bool
	cnameLookup func(context.Context, string) (string, error)
}

func NewService(store *storage.Store, postalCNAME string, dnsCheck bool) *Service {
	resolver := net.DefaultResolver
	return &Service{
		store:       store,
		postalCNAME: postalCNAME,
		dnsCheck:    dnsCheck,
		cnameLookup: resolver.LookupCNAME,
	}
}

func (s *Service) Create(ctx context.Context, request sendgrid.DomainRequest) (sendgrid.DomainResponse, error) {
	subdomain := request.Subdomain
	if subdomain == "" {
		subdomain = "mail"
	}
	records := []storage.DomainRecord{{
		Key:    mailCNAMEKey,
		Type:   "cname",
		Host:   fmt.Sprintf("%s.%s", subdomain, request.Domain),
		Data:   s.postalCNAME,
		Reason: "DNS record not checked",
	}}
	preview := sendgrid.DomainResponse{Domain: request.Domain, Subdomain: subdomain, Valid: false, DNS: dnsRecords(records)}
	stored, err := s.store.CreateDomain(ctx, request.Domain, subdomain, records, preview)
	if err != nil {
		return sendgrid.DomainResponse{}, err
	}
	return domainResponse(stored), nil
}

func (s *Service) Validate(ctx context.Context, id int64) (sendgrid.ValidateResponse, error) {
	stored, err := s.store.GetDomain(ctx, id)
	if err != nil {
		return sendgrid.ValidateResponse{}, err
	}

	records := make([]storage.DomainRecord, len(stored.Records))
	valid := true
	for index, record := range stored.Records {
		updated := record
		updated.Valid, updated.Reason = s.validateRecord(ctx, record)
		records[index] = updated
		valid = valid && updated.Valid
	}

	response := validateResponse(valid, records)
	_, err = s.store.UpdateDomainValidation(ctx, id, records, valid, response)
	if err != nil {
		return sendgrid.ValidateResponse{}, err
	}
	return response, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.store.DeleteDomain(ctx, id)
}

func (s *Service) validateRecord(ctx context.Context, record storage.DomainRecord) (bool, string) {
	if !s.dnsCheck {
		return false, "DNS checking is disabled"
	}
	if strings.ToLower(record.Type) != "cname" {
		panic(fmt.Sprintf("unsupported DNS record type: %s", record.Type))
	}
	cname, err := s.cnameLookup(ctx, record.Host)
	if err != nil {
		return false, "DNS record not found"
	}
	if normalizeDNSName(cname) != normalizeDNSName(record.Data) {
		return false, fmt.Sprintf("DNS record must point to %s", record.Data)
	}
	return true, ""
}

func domainResponse(domain storage.Domain) sendgrid.DomainResponse {
	return sendgrid.DomainResponse{
		ID:        domain.ID,
		Domain:    domain.Domain,
		Subdomain: domain.Subdomain,
		Valid:     domain.Valid,
		DNS:       dnsRecords(domain.Records),
	}
}

func dnsRecords(records []storage.DomainRecord) map[string]sendgrid.DNSRecord {
	values := make(map[string]sendgrid.DNSRecord, len(records))
	for _, record := range records {
		values[record.Key] = sendgrid.DNSRecord{Type: record.Type, Host: record.Host, Data: record.Data, Valid: record.Valid}
	}
	return values
}

func validateResponse(valid bool, records []storage.DomainRecord) sendgrid.ValidateResponse {
	results := make(map[string]sendgrid.ValidationRecord, len(records))
	for _, record := range records {
		results[record.Key] = sendgrid.ValidationRecord{Valid: record.Valid, Reason: record.Reason}
	}
	return sendgrid.ValidateResponse{Valid: valid, ValidationResults: results}
}

func normalizeDNSName(value string) string {
	return strings.TrimSuffix(strings.ToLower(value), ".")
}
