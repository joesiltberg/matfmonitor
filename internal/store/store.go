// Package store provides SQLite persistence for server health status.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ServerStatus represents the health check status of a server
type ServerStatus struct {
	EntityID        string
	BaseURI         string
	LastChecked     *time.Time
	IsHealthy       *bool
	ErrorMessage    string
	CertExpires     *time.Time
	CertCN          string
	CertFingerprint string
}

// Store provides persistence for server health status
type Store struct {
	db *sql.DB
}

// New creates a new Store and initializes the database schema
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

func initSchema(db *sql.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS server_status (
			entity_id TEXT NOT NULL,
			base_uri TEXT NOT NULL,
			last_checked TIMESTAMP,
			is_healthy BOOLEAN,
			error_message TEXT,
			cert_expires TIMESTAMP,
			cert_cn TEXT,
			cert_fingerprint TEXT,
			PRIMARY KEY (entity_id, base_uri)
		);

		CREATE INDEX IF NOT EXISTS idx_last_checked ON server_status(last_checked);
	`
	_, err := db.Exec(schema)
	return err
}

// SaveStatus saves or updates a server's health status
func (s *Store) SaveStatus(status *ServerStatus) error {
	query := `
		INSERT INTO server_status (
			entity_id, base_uri, last_checked, is_healthy, error_message,
			cert_expires, cert_cn, cert_fingerprint
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_id, base_uri) DO UPDATE SET
			last_checked = excluded.last_checked,
			is_healthy = excluded.is_healthy,
			error_message = excluded.error_message,
			cert_expires = excluded.cert_expires,
			cert_cn = excluded.cert_cn,
			cert_fingerprint = excluded.cert_fingerprint
	`
	_, err := s.db.Exec(query,
		status.EntityID, status.BaseURI, status.LastChecked, status.IsHealthy,
		status.ErrorMessage, status.CertExpires, status.CertCN, status.CertFingerprint,
	)
	return err
}

// GetStatus retrieves a server's health status
func (s *Store) GetStatus(entityID, baseURI string) (*ServerStatus, error) {
	query := `
		SELECT entity_id, base_uri, last_checked, is_healthy, error_message,
		       cert_expires, cert_cn, cert_fingerprint
		FROM server_status
		WHERE entity_id = ? AND base_uri = ?
	`
	status := &ServerStatus{}
	var errorMessage, certCN, certFingerprint sql.NullString
	err := s.db.QueryRow(query, entityID, baseURI).Scan(
		&status.EntityID, &status.BaseURI, &status.LastChecked, &status.IsHealthy,
		&errorMessage, &status.CertExpires, &certCN, &certFingerprint,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	status.ErrorMessage = errorMessage.String
	status.CertCN = certCN.String
	status.CertFingerprint = certFingerprint.String
	return status, nil
}

// GetAllStatuses retrieves all server statuses
func (s *Store) GetAllStatuses() ([]*ServerStatus, error) {
	query := `
		SELECT entity_id, base_uri, last_checked, is_healthy, error_message,
		       cert_expires, cert_cn, cert_fingerprint
		FROM server_status
		ORDER BY entity_id, base_uri
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []*ServerStatus
	for rows.Next() {
		status := &ServerStatus{}
		var errorMessage, certCN, certFingerprint sql.NullString
		if err := rows.Scan(
			&status.EntityID, &status.BaseURI, &status.LastChecked, &status.IsHealthy,
			&errorMessage, &status.CertExpires, &certCN, &certFingerprint,
		); err != nil {
			return nil, err
		}
		status.ErrorMessage = errorMessage.String
		status.CertCN = certCN.String
		status.CertFingerprint = certFingerprint.String
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}

// ServerToCheck represents a server that may need checking
type ServerToCheck struct {
	EntityID    string
	BaseURI     string
	LastChecked *time.Time
}

// GetServersNeedingCheck returns servers that haven't been checked recently,
// ordered by last_checked (oldest first, NULL first)
func (s *Store) GetServersNeedingCheck(minInterval time.Duration, limit int) ([]*ServerToCheck, error) {
	cutoff := time.Now().Add(-minInterval)
	query := `
		SELECT entity_id, base_uri, last_checked
		FROM server_status
		WHERE last_checked IS NULL OR last_checked < ?
		ORDER BY last_checked IS NOT NULL, last_checked ASC
		LIMIT ?
	`
	rows, err := s.db.Query(query, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*ServerToCheck
	for rows.Next() {
		server := &ServerToCheck{}
		if err := rows.Scan(&server.EntityID, &server.BaseURI, &server.LastChecked); err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

// EnsureServerExists creates a server_status row if it doesn't exist
func (s *Store) EnsureServerExists(entityID, baseURI string) error {
	query := `
		INSERT OR IGNORE INTO server_status (entity_id, base_uri)
		VALUES (?, ?)
	`
	_, err := s.db.Exec(query, entityID, baseURI)
	return err
}

// RemoveServersNotIn removes servers that are not in the provided list
func (s *Store) RemoveServersNotIn(servers []struct{ EntityID, BaseURI string }) error {
	if len(servers) == 0 {
		// Don't delete everything if list is empty (metadata might be temporarily unavailable)
		return nil
	}

	// Build a temporary table of current servers
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`CREATE TEMPORARY TABLE current_servers (entity_id TEXT, base_uri TEXT)`)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO current_servers VALUES (?, ?)`)
	if err != nil {
		return err
	}
	for _, s := range servers {
		if _, err := stmt.Exec(s.EntityID, s.BaseURI); err != nil {
			stmt.Close()
			return err
		}
	}
	stmt.Close()

	_, err = tx.Exec(`
		DELETE FROM server_status
		WHERE NOT EXISTS (
			SELECT 1 FROM current_servers
			WHERE current_servers.entity_id = server_status.entity_id
			AND current_servers.base_uri = server_status.base_uri
		)
	`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DROP TABLE current_servers`)
	if err != nil {
		return err
	}

	return tx.Commit()
}
