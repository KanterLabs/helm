package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// PublicEndpoint records the provider resources behind a public webhook
// hostname. It never holds a credential.
type PublicEndpoint struct {
	ID             string  `json:"id"`
	Provider       string  `json:"provider"`
	Hostname       string  `json:"hostname"`
	AccountID      string  `json:"account_id"`
	ZoneID         string  `json:"zone_id"`
	TunnelID       string  `json:"tunnel_id"`
	DNSRecordID    string  `json:"dns_record_id"`
	Status         string  `json:"status"`
	CleanupPending bool    `json:"cleanup_pending"`
	CreatedAt      string  `json:"created_at"`
	DisabledAt     *string `json:"disabled_at,omitempty"`
}

const publicEndpointSelect = `SELECT id, provider, hostname, account_id, zone_id, tunnel_id, dns_record_id, status, cleanup_pending, created_at, disabled_at FROM public_endpoints`

func publicEndpointFromRow(scanner interface{ Scan(...any) error }) (PublicEndpoint, error) {
	var endpoint PublicEndpoint
	var cleanup int
	var disabled sql.NullString
	if err := scanner.Scan(&endpoint.ID, &endpoint.Provider, &endpoint.Hostname, &endpoint.AccountID, &endpoint.ZoneID, &endpoint.TunnelID, &endpoint.DNSRecordID, &endpoint.Status, &cleanup, &endpoint.CreatedAt, &disabled); err != nil {
		return PublicEndpoint{}, err
	}
	endpoint.CleanupPending, endpoint.DisabledAt = cleanup == 1, nullableString(disabled)
	return endpoint, nil
}

// ActivePublicEndpoint returns the single active endpoint, if any.
func (s *Store) ActivePublicEndpoint(ctx context.Context) (PublicEndpoint, bool, error) {
	endpoint, err := publicEndpointFromRow(s.DB.QueryRowContext(ctx, publicEndpointSelect+` WHERE status = 'active'`))
	if errors.Is(err, sql.ErrNoRows) {
		return PublicEndpoint{}, false, nil
	}
	return endpoint, err == nil, err
}

func (s *Store) ListPublicEndpoints(ctx context.Context) ([]PublicEndpoint, error) {
	rows, err := s.DB.QueryContext(ctx, publicEndpointSelect+` ORDER BY status = 'active' DESC, created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	endpoints := []PublicEndpoint{}
	for rows.Next() {
		endpoint, err := publicEndpointFromRow(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, rows.Err()
}

func (s *Store) GetPublicEndpoint(ctx context.Context, id string) (PublicEndpoint, error) {
	endpoint, err := publicEndpointFromRow(s.DB.QueryRowContext(ctx, publicEndpointSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PublicEndpoint{}, notFound("public endpoint not found")
	}
	return endpoint, err
}

// CreatePublicEndpoint stores a provisioned endpoint as the active one.
func (s *Store) CreatePublicEndpoint(ctx context.Context, endpoint PublicEndpoint, actorID string) (PublicEndpoint, error) {
	if endpoint.ID == "" {
		endpoint.ID = newID()
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public_endpoints(id, provider, hostname, account_id, zone_id, tunnel_id, dns_record_id, status, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', NULLIF(?, ''), ?)`,
			endpoint.ID, endpoint.Provider, endpoint.Hostname, endpoint.AccountID, endpoint.ZoneID, endpoint.TunnelID, endpoint.DNSRecordID, actorID, now()); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return conflict("a public endpoint is already active; disable it first", nil)
			}
			return err
		}
		_, err := insertEvent(ctx, tx, "public_endpoint.created", actorID, "", "", map[string]any{"endpoint_id": endpoint.ID, "hostname": endpoint.Hostname, "provider": endpoint.Provider})
		return err
	})
	if err != nil {
		return PublicEndpoint{}, err
	}
	return s.GetPublicEndpoint(ctx, endpoint.ID)
}

// DisablePublicEndpoint marks the endpoint disabled; cleanupPending records
// that provider resources still exist because no token was supplied.
func (s *Store) DisablePublicEndpoint(ctx context.Context, id, actorID string, cleanupPending bool) (PublicEndpoint, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE public_endpoints SET status = 'disabled', disabled_at = COALESCE(disabled_at, ?), cleanup_pending = ? WHERE id = ?`, now(), boolInt(cleanupPending), id)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return notFound("public endpoint not found")
		}
		_, err = insertEvent(ctx, tx, "public_endpoint.disabled", actorID, "", "", map[string]any{"endpoint_id": id, "cleanup_pending": cleanupPending})
		return err
	})
	if err != nil {
		return PublicEndpoint{}, err
	}
	return s.GetPublicEndpoint(ctx, id)
}
