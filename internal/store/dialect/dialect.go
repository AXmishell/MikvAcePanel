// Package dialect contains the small set of SQL differences the panel needs
// to talk to more than one database engine.
//
// Store code keeps writing "?" placeholders and plain SQL; the dialect layer
// rewrites placeholders, quotes identifiers and classifies driver errors.
// SQLite stays the default backend. PostgreSQL and MySQL support is added
// incrementally on top of this package.
package dialect

import (
	"errors"
	"fmt"
	"strings"
)

// Driver names accepted by For.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
	MySQL    = "mysql"
)

// Dialect describes one database engine.
//
// The interface intentionally stays small in the first phase. DDL generation,
// upserts and schema introspection are added once the wrapper layer is in
// place.
type Dialect interface {
	// Name is the canonical backend name: sqlite, postgres or mysql.
	Name() string
	// DriverName is the database/sql driver name passed to sql.Open.
	DriverName() string
	// Rebind rewrites "?" placeholders for the engine.
	Rebind(query string) string
	// Quote quotes an identifier for safe interpolation by DDL builders.
	Quote(identifier string) string
	// IsUniqueViolation reports whether err is a duplicate-key error.
	IsUniqueViolation(err error) bool
	// IsRetryable reports whether the statement can be retried after a
	// transient serialization or connection failure.
	IsRetryable(err error) bool
}

// For resolves a backend name. An empty name selects SQLite.
func For(name string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", SQLite, "sqlite3":
		return SQLiteDialect{}, nil
	case Postgres, "postgresql", "pg", "pgx":
		return PostgresDialect{}, nil
	case MySQL, "mariadb":
		return MySQLDialect{}, nil
	}
	return nil, fmt.Errorf("unsupported database driver %q (expected sqlite, postgres or mysql)", name)
}

// ForDSN infers the backend from a URL scheme. It returns an empty string when
// the DSN has no scheme, so the caller can keep its configured default.
func ForDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "sqlite:") {
		return SQLite
	}
	scheme, _, ok := strings.Cut(dsn, "://")
	if !ok {
		return ""
	}
	switch strings.ToLower(scheme) {
	case "postgres", "postgresql", "pg":
		return Postgres
	case "mysql", "mariadb":
		return MySQL
	case "sqlite", "sqlite3", "file":
		return SQLite
	}
	return ""
}

// sqlState is implemented by pgconn.PgError and most other drivers that expose
// five-character SQLSTATE codes.
type sqlState interface {
	SQLState() string
}

func sqlStateOf(err error) string {
	var e sqlState
	if errors.As(err, &e) {
		return e.SQLState()
	}
	return ""
}

// rebindToDollar converts "?" placeholders to PostgreSQL "$1" style while
// leaving question marks inside string literals, quoted identifiers and
// comments untouched.
func rebindToDollar(query string) string {
	if !strings.ContainsRune(query, '?') {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); {
		c := query[i]
		switch c {
		case '\'', '"', '`':
			// Copy the quoted run verbatim. SQL escapes the quote by doubling it.
			quote := c
			b.WriteByte(c)
			i++
			for i < len(query) {
				b.WriteByte(query[i])
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						b.WriteByte(query[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				for i < len(query) && query[i] != '\n' {
					b.WriteByte(query[i])
					i++
				}
				continue
			}
			b.WriteByte(c)
			i++
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				depth := 0
				for i < len(query) {
					if i+1 < len(query) && query[i] == '/' && query[i+1] == '*' {
						depth++
						b.WriteString("/*")
						i += 2
						continue
					}
					if i+1 < len(query) && query[i] == '*' && query[i+1] == '/' {
						depth--
						b.WriteString("*/")
						i += 2
						if depth == 0 {
							break
						}
						continue
					}
					b.WriteByte(query[i])
					i++
				}
				continue
			}
			b.WriteByte(c)
			i++
		case '$':
			if end, ok := dollarQuoteEnd(query, i); ok {
				b.WriteString(query[i:end])
				i = end
				continue
			}
			b.WriteByte(c)
			i++
		case '?':
			n++
			b.WriteByte('$')
			b.WriteString(itoa(n))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// dollarQuoteEnd recognises PostgreSQL dollar-quoted strings such as
// $$body$$ or $tag$body$tag$. It returns the index just past the closing tag.
func dollarQuoteEnd(query string, start int) (int, bool) {
	j := start + 1
	for j < len(query) && (isIdentByte(query[j])) {
		j++
	}
	if j >= len(query) || query[j] != '$' {
		return 0, false
	}
	tag := query[start : j+1]
	rest := query[j+1:]
	idx := strings.Index(rest, tag)
	if idx < 0 {
		return 0, false
	}
	return j + 1 + idx + len(tag), true
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
