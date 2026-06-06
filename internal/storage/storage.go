package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrDuplicateDomain = errors.New("domain already exists")
	ErrDomainNotFound  = errors.New("domain not found")
	ErrEventDuplicate  = errors.New("event already forwarded")
)

type Store struct {
	db *sql.DB
}

type Domain struct {
	ID        int64
	Domain    string
	Subdomain string
	Valid     bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Records   []DomainRecord
}

type DomainRecord struct {
	ID       int64
	DomainID int64
	Key      string
	Type     string
	Host     string
	Data     string
	Valid    bool
	Reason   string
}

type MessageMapping struct {
	ShimMessageID        string
	PostalMessageID      string
	PostalMessageToken   string
	PlunkEmailID         string
	PlunkProjectID       string
	Recipient            string
	Sender               string
	Subject              string
	CustomArgsJSON       string
	TrackingOpenEnabled  bool
	TrackingClickEnabled bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS domains (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            domain TEXT NOT NULL,
            subdomain TEXT NOT NULL,
            valid INTEGER NOT NULL DEFAULT 0,
            provider_data_json TEXT NOT NULL DEFAULT '{}',
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            UNIQUE(domain, subdomain)
        )`,
		`CREATE TABLE IF NOT EXISTS domain_records (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            domain_id INTEGER NOT NULL,
            record_key TEXT NOT NULL,
            type TEXT NOT NULL,
            host TEXT NOT NULL,
            data TEXT NOT NULL,
            valid INTEGER NOT NULL DEFAULT 0,
            reason TEXT NOT NULL DEFAULT '',
            FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE,
            UNIQUE(domain_id, record_key)
        )`,
		`CREATE TABLE IF NOT EXISTS message_mappings (
            shim_message_id TEXT PRIMARY KEY,
            postal_message_id TEXT NOT NULL DEFAULT '',
            postal_message_token TEXT NOT NULL DEFAULT '',
            plunk_email_id TEXT NOT NULL DEFAULT '',
            plunk_project_id TEXT NOT NULL DEFAULT '',
            recipient TEXT NOT NULL,
            sender TEXT NOT NULL,
            subject TEXT NOT NULL,
            custom_args_json TEXT NOT NULL DEFAULT '{}',
            tracking_open_enabled INTEGER NOT NULL DEFAULT 1,
            tracking_click_enabled INTEGER NOT NULL DEFAULT 1,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_message_mappings_postal_message_id ON message_mappings(postal_message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_mappings_postal_token ON message_mappings(postal_message_token)`,
		`CREATE TABLE IF NOT EXISTS forwarded_events (
            provider_event_id TEXT PRIMARY KEY,
            shim_message_id TEXT NOT NULL,
            postal_event_type TEXT NOT NULL,
            forwarded_at TEXT NOT NULL,
            payload_json TEXT NOT NULL
        )`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateDomain(ctx context.Context, domain string, subdomain string, records []DomainRecord, providerData any) (Domain, error) {
	now := time.Now().UTC()
	providerJSON, err := marshalJSON(providerData)
	if err != nil {
		return Domain{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Domain{}, err
	}
	defer rollback(tx)

	result, err := tx.ExecContext(ctx, `INSERT INTO domains(domain, subdomain, valid, provider_data_json, created_at, updated_at) VALUES (?, ?, 0, ?, ?, ?)`, domain, subdomain, providerJSON, formatTime(now), formatTime(now))
	if err != nil {
		if isUniqueConstraint(err) {
			return Domain{}, ErrDuplicateDomain
		}
		return Domain{}, err
	}
	domainID, err := result.LastInsertId()
	if err != nil {
		return Domain{}, err
	}
	for _, record := range records {
		_, err = tx.ExecContext(ctx, `INSERT INTO domain_records(domain_id, record_key, type, host, data, valid, reason) VALUES (?, ?, ?, ?, ?, 0, ?)`, domainID, record.Key, record.Type, record.Host, record.Data, record.Reason)
		if err != nil {
			return Domain{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Domain{}, err
	}

	created := Domain{ID: domainID, Domain: domain, Subdomain: subdomain, Valid: false, CreatedAt: now, UpdatedAt: now, Records: records}
	for index := range created.Records {
		created.Records[index].DomainID = domainID
	}
	return created, nil
}

func (s *Store) GetDomain(ctx context.Context, id int64) (Domain, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, domain, subdomain, valid, created_at, updated_at FROM domains WHERE id = ?`, id)
	domain, err := scanDomain(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Domain{}, ErrDomainNotFound
		}
		return Domain{}, err
	}
	records, err := s.getDomainRecords(ctx, id)
	if err != nil {
		return Domain{}, err
	}
	domain.Records = records
	return domain, nil
}

func (s *Store) DeleteDomain(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrDomainNotFound
	}
	return nil
}

func (s *Store) UpdateDomainValidation(ctx context.Context, id int64, records []DomainRecord, valid bool, providerData any) (Domain, error) {
	now := time.Now().UTC()
	providerJSON, err := marshalJSON(providerData)
	if err != nil {
		return Domain{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Domain{}, err
	}
	defer rollback(tx)

	result, err := tx.ExecContext(ctx, `UPDATE domains SET valid = ?, provider_data_json = ?, updated_at = ? WHERE id = ?`, boolToInt(valid), providerJSON, formatTime(now), id)
	if err != nil {
		return Domain{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Domain{}, err
	}
	if rows == 0 {
		return Domain{}, ErrDomainNotFound
	}
	for _, record := range records {
		_, err = tx.ExecContext(ctx, `UPDATE domain_records SET valid = ?, reason = ? WHERE domain_id = ? AND record_key = ?`, boolToInt(record.Valid), record.Reason, id, record.Key)
		if err != nil {
			return Domain{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Domain{}, err
	}
	return s.GetDomain(ctx, id)
}

func (s *Store) SaveMessageMapping(ctx context.Context, mapping MessageMapping) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO message_mappings(
        shim_message_id, postal_message_id, postal_message_token, plunk_email_id, plunk_project_id,
        recipient, sender, subject, custom_args_json, tracking_open_enabled, tracking_click_enabled, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mapping.ShimMessageID, mapping.PostalMessageID, mapping.PostalMessageToken, mapping.PlunkEmailID, mapping.PlunkProjectID,
		mapping.Recipient, mapping.Sender, mapping.Subject, mapping.CustomArgsJSON, boolToInt(mapping.TrackingOpenEnabled), boolToInt(mapping.TrackingClickEnabled), formatTime(now), formatTime(now))
	return err
}

func (s *Store) FindMessageMapping(ctx context.Context, shimMessageID string, postalMessageID string, postalMessageToken string) (MessageMapping, bool, error) {
	query := `SELECT shim_message_id, postal_message_id, postal_message_token, plunk_email_id, plunk_project_id, recipient, sender, subject, custom_args_json, tracking_open_enabled, tracking_click_enabled, created_at, updated_at FROM message_mappings WHERE `
	args := make([]any, 0, 3)
	switch {
	case shimMessageID != "":
		query += `shim_message_id = ?`
		args = append(args, shimMessageID)
	case postalMessageID != "":
		query += `postal_message_id = ?`
		args = append(args, postalMessageID)
	case postalMessageToken != "":
		query += `postal_message_token = ?`
		args = append(args, postalMessageToken)
	default:
		return MessageMapping{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, query, args...)
	mapping, err := scanMapping(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MessageMapping{}, false, nil
		}
		return MessageMapping{}, false, err
	}
	return mapping, true, nil
}

func (s *Store) HasForwardedEvent(ctx context.Context, providerEventID string) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT provider_event_id FROM forwarded_events WHERE provider_event_id = ?`, providerEventID).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) RecordForwardedEvent(ctx context.Context, providerEventID string, shimMessageID string, postalEventType string, payload any) error {
	payloadJSON, err := marshalJSON(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO forwarded_events(provider_event_id, shim_message_id, postal_event_type, forwarded_at, payload_json) VALUES (?, ?, ?, ?, ?)`, providerEventID, shimMessageID, postalEventType, formatTime(time.Now().UTC()), payloadJSON)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrEventDuplicate
		}
		return err
	}
	return nil
}

func (s *Store) getDomainRecords(ctx context.Context, domainID int64) ([]DomainRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, domain_id, record_key, type, host, data, valid, reason FROM domain_records WHERE domain_id = ? ORDER BY id`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DomainRecord
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDomain(row rowScanner) (Domain, error) {
	var domain Domain
	var valid int
	var created string
	var updated string
	if err := row.Scan(&domain.ID, &domain.Domain, &domain.Subdomain, &valid, &created, &updated); err != nil {
		return Domain{}, err
	}
	domain.Valid = valid == 1
	domain.CreatedAt = parseTime(created)
	domain.UpdatedAt = parseTime(updated)
	return domain, nil
}

func scanRecord(row rowScanner) (DomainRecord, error) {
	var record DomainRecord
	var valid int
	if err := row.Scan(&record.ID, &record.DomainID, &record.Key, &record.Type, &record.Host, &record.Data, &valid, &record.Reason); err != nil {
		return DomainRecord{}, err
	}
	record.Valid = valid == 1
	return record, nil
}

func scanMapping(row rowScanner) (MessageMapping, error) {
	var mapping MessageMapping
	var openEnabled int
	var clickEnabled int
	var created string
	var updated string
	err := row.Scan(&mapping.ShimMessageID, &mapping.PostalMessageID, &mapping.PostalMessageToken, &mapping.PlunkEmailID, &mapping.PlunkProjectID, &mapping.Recipient, &mapping.Sender, &mapping.Subject, &mapping.CustomArgsJSON, &openEnabled, &clickEnabled, &created, &updated)
	if err != nil {
		return MessageMapping{}, err
	}
	mapping.TrackingOpenEnabled = openEnabled == 1
	mapping.TrackingClickEnabled = clickEnabled == 1
	mapping.CreatedAt = parseTime(created)
	mapping.UpdatedAt = parseTime(updated)
	return mapping, nil
}

func marshalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func formatTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(fmt.Sprintf("stored timestamp is invalid: %s", value))
	}
	return parsed
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "constraint failed") || strings.Contains(err.Error(), "UNIQUE constraint failed"))
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
