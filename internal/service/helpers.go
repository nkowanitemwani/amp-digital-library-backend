package service

import "strings"

// =============================================================
// POSTGRES ERROR HELPERS
// These helpers are used across multiple service files.
// Centralising them here means the check logic is defined once
// and any service file in this package can call them directly.
//
// We check error message substrings rather than doing a type
// assertion to pq.Error because that would import the postgres
// driver into every service file — keeping these as string checks
// avoids coupling the service layer to a specific DB driver.
// =============================================================

// isUniqueViolation returns true when a DB error is caused by a
// UNIQUE constraint violation (Postgres error code 23505).
// Used to detect duplicate emails, usernames, category names, and
// unit numbers before returning a clean error to the handler.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

// isForeignKeyViolation returns true when a DB error is caused by a
// FOREIGN KEY constraint violation (Postgres error code 23503).
// This occurs when trying to delete a category that still has books,
// or any other operation that would orphan a child row.
func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23503")
}