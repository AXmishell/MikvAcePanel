package dialect

import "strings"

// MySQLDialect targets MySQL 8.0. Placeholders stay "?"; identifiers use
// backticks because several MySQL keywords collide with the panel schema.
type MySQLDialect struct{}

func (MySQLDialect) Name() string       { return MySQL }
func (MySQLDialect) DriverName() string { return "mysql" }
func (MySQLDialect) Rebind(q string) string {
	return q
}
func (MySQLDialect) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}
func (MySQLDialect) IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "error 1062") || strings.Contains(msg, "duplicate entry")
}
func (MySQLDialect) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if sqlStateOf(err) == "40001" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return containsAny(msg, "error 1213", "error 1205", "error 1040", "error 2006", "error 2013",
		"deadlock found", "lock wait timeout", "connection refused", "connection reset")
}
