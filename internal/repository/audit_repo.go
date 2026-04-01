package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// AuditRepository handles inserts into the audit_log table.
// The audit log is append-only — this repository intentionally
// exposes no update, delete, or read methods. Once written,
// audit entries are permanent.
type AuditRepository struct {
	db *sql.DB
}

// NewAuditRepository creates an AuditRepository with the shared DB pool.
func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Log writes a single audit entry. It is deliberately fire-and-forget —
// a failure to write an audit log entry should never cause the calling
// operation to fail. Errors are logged but not returned.
//
// This is the only place in the codebase that writes to audit_log,
// making it easy to audit the auditor itself.
func (r *AuditRepository) Log(ctx context.Context, entry *models.AuditEntry) {
	// Serialise the metadata map to JSON for the JSONB column.
	// A nil metadata map is stored as SQL NULL, not "null" — this
	// keeps the column clean for rows that have no extra context.
	var metaJSON []byte
	if len(entry.Metadata) > 0 {
		var err error
		metaJSON, err = json.Marshal(entry.Metadata)
		if err != nil {
			// Marshal of a map[string]string should never fail, but if it
			// does we log and continue rather than dropping the audit entry.
			log.Printf("audit: failed to marshal metadata for action %q: %v", entry.Action, err)
		}
	}

	query := `
		INSERT INTO audit_log (school_id, action, entity, entity_id, metadata)
		VALUES ($1, $2, $3, $4, $5)`

	_, err := r.db.ExecContext(ctx, query,
		entry.SchoolID,
		entry.Action,
		entry.Entity,
		entry.EntityID,
		nullableJSON(metaJSON),
	)
	if err != nil {
		// Audit failures are logged, not propagated — the calling operation
		// has already succeeded and should not be rolled back because of this.
		log.Printf("audit: failed to write entry for action %q: %v", entry.Action, err)
	}
}

// nullableJSON converts a byte slice to a sql.NullString so that an
// empty slice is stored as SQL NULL rather than an empty string.
func nullableJSON(b []byte) sql.NullString {
	if len(b) == 0 {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: fmt.Sprintf("%s", b), Valid: true}
}