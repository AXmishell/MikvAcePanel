package dialect

import "strings"

// SQLiteDialect is the default backend. Its SQL already uses "?" placeholders
// and double-quoted identifiers, so most operations are pass-through.
type SQLiteDialect struct{}

func (SQLiteDialect) Name() string       { return SQLite }
func (SQLiteDialect) DriverName() string { return "sqlite" }
func (SQLiteDialect) Rebind(q string) string {
	return q
}
func (SQLiteDialect) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (SQLiteDialect) IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
func (SQLiteDialect) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return containsAny(msg, "database is locked", "database table is locked", "sqlite_busy", "sqlite_locked")
}
