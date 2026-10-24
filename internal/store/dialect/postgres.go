package dialect

import "strings"

// PostgresDialect targets PostgreSQL 18 (and older releases with the same
// SQL surface). The pgx driver is used through database/sql.
type PostgresDialect struct{}

func (PostgresDialect) Name() string       { return Postgres }
func (PostgresDialect) DriverName() string { return "pgx" }
func (PostgresDialect) Rebind(q string) string {
	return rebindToDollar(q)
}
func (PostgresDialect) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (PostgresDialect) IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if sqlStateOf(err) == "23505" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate key value violates unique constraint")
}
func (PostgresDialect) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	switch sqlStateOf(err) {
	case "40001", "40P01", "57P01", "57P02", "57P03":
		return true
	}
	msg := strings.ToLower(err.Error())
	return containsAny(msg, "connection refused", "connection reset", "bad connection", "server closed the connection")
}
