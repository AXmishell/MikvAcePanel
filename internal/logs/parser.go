// Package logs parses and analyses Hysteria 2 log output.
package logs

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Parse converts one raw Hysteria 2 log line into a structured entry.
// fallback is used when the line has no parseable timestamp (for example a
// process startup message emitted by the agent itself).
func Parse(raw string, fallback time.Time) model.LogEntry {
	raw = strings.TrimRight(ansiRE.ReplaceAllString(raw, ""), "\r\n")
	entry := model.LogEntry{Raw: raw, TS: fallback}
	if entry.TS.IsZero() {
		entry.TS = time.Now().UTC()
	}
	line := strings.TrimSpace(raw)
	if line == "" {
		return entry
	}
	if xrayEntry, ok := parseXrayLine(line, fallback); ok {
		return xrayEntry
	}

	header, fields := splitFields(line)
	parts := strings.Split(header, "\t")
	if len(parts) == 1 {
		// Try logfmt style output as a fallback.
		if strings.Contains(header, "level=") || strings.Contains(header, "msg=") {
			return parseLogfmt(line, fallback)
		}
		entry.Message = strings.TrimSpace(header)
	} else {
		if t := parseTimestamp(strings.TrimSpace(parts[0])); !t.IsZero() {
			entry.TS = t
		}
		if len(parts) >= 2 {
			entry.Level = normalizeLevel(parts[1])
		}
		switch {
		case len(parts) >= 4:
			entry.Logger = strings.TrimSpace(parts[2])
			entry.Message = strings.TrimSpace(strings.Join(parts[3:], " "))
		case len(parts) == 3:
			entry.Message = strings.TrimSpace(parts[2])
		default:
			entry.Message = strings.TrimSpace(strings.Join(parts[1:], " "))
		}
	}
	if entry.Message == "" {
		entry.Message = line
	}
	applyFields(&entry, fields)
	finishClassification(&entry)
	return entry
}

func splitFields(line string) (string, map[string]any) {
	idx := strings.Index(line, "\t{")
	if idx < 0 {
		idx = strings.Index(line, " {")
	}
	if idx < 0 {
		return line, nil
	}
	header := line[:idx]
	payload := strings.TrimSpace(line[idx:])
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		return line, nil
	}
	return header, fields
}

func parseLogfmt(line string, fallback time.Time) model.LogEntry {
	e := model.LogEntry{Raw: line, TS: fallback}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	fields := map[string]any{}
	for _, tok := range tokenizeLogfmt(line) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "time", "ts":
			if t := parseTimestamp(v); !t.IsZero() {
				e.TS = t
			}
		case "level":
			e.Level = normalizeLevel(v)
		case "logger":
			e.Logger = v
		case "msg", "message":
			e.Message = v
		default:
			fields[k] = v
		}
	}
	applyFields(&e, fields)
	finishClassification(&e)
	return e
}

func tokenizeLogfmt(s string) []string {
	out := []string{}
	var b strings.Builder
	inQuote := false
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			b.WriteRune(r)
			escaped = true
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ' ' && !inQuote:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func parseTimestamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000Z0700",
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05.000",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	// Unix seconds / milliseconds (some wrappers prefix logs this way).
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n).UTC()
		}
		return time.Unix(n, 0).UTC()
	}
	return time.Time{}
}

func normalizeLevel(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG", "TRACE":
		return "DEBUG"
	case "INFO":
		return "INFO"
	case "WARN", "WARNING":
		return "WARN"
	case "ERROR":
		return "ERROR"
	case "FATAL", "PANIC", "DPANIC":
		return "FATAL"
	case "":
		return ""
	default:
		return strings.ToUpper(strings.TrimSpace(s))
	}
}

func applyFields(e *model.LogEntry, fields map[string]any) {
	if fields == nil {
		return
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := fields[k]; ok {
				switch x := v.(type) {
				case string:
					return x
				case json.Number:
					return x.String()
				case float64:
					return strconv.FormatFloat(x, 'f', -1, 64)
				case bool:
					return strconv.FormatBool(x)
				default:
					return fmt.Sprint(x)
				}
			}
		}
		return ""
	}
	addr := get("addr", "client", "remote", "remoteAddr", "address")
	if addr != "" {
		host, port := splitHostPort(addr)
		if host != "" {
			e.ClientIP = host
			e.Port = port
		}
	}
	e.UserID = get("id", "auth", "user", "userId")
	e.Host = get("reqAddr", "req_addr", "host", "destination", "dst")
	if e.Host != "" && e.Port == 0 {
		_, p := splitHostPort(e.Host)
		if p > 0 {
			e.Port = p
		}
	}
	e.Error = get("error", "err", "reason")
	if v := get("tx", "upload"); v != "" {
		if n, err := strconv.ParseInt(strings.Split(v, ".")[0], 10, 64); err == nil {
			e.BytesTx = n
		}
	}
	if v := get("rx", "download"); v != "" {
		if n, err := strconv.ParseInt(strings.Split(v, ".")[0], 10, 64); err == nil {
			e.BytesRx = n
		}
	}
	if v := get("duration", "latency", "elapsed"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			e.DurationMS = d.Milliseconds()
		} else if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			e.DurationMS = n
		}
	}
}

func splitHostPort(addr string) (string, int) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err == nil {
		p, _ := strconv.Atoi(portStr)
		return strings.Trim(host, "[]"), p
	}
	// A bare IP or hostname.
	return strings.Trim(addr, "[]"), 0
}

func finishClassification(e *model.LogEntry) {
	msg := strings.ToLower(e.Message)
	event := ""
	switch {
	case strings.Contains(msg, "authentication succeeded"), strings.Contains(msg, "auth succeeded"),
		strings.Contains(msg, "auth success"):
		event = "auth_success"
	case strings.Contains(msg, "authentication failed"), strings.Contains(msg, "auth failed"),
		strings.Contains(msg, "failed to authenticate"), strings.Contains(msg, "authentication error"),
		strings.Contains(msg, "unauthorized"):
		event = "auth_fail"
	case strings.Contains(msg, "client connected"):
		event = "connect"
	case strings.Contains(msg, "client disconnected"):
		event = "disconnect"
	case strings.Contains(msg, "server up and running"), strings.Contains(msg, "server started"):
		event = "start"
	case strings.Contains(msg, "shutting down"), strings.Contains(msg, "received signal"), strings.Contains(msg, "server stopped"):
		event = "shutdown"
	case strings.Contains(msg, "traffic stats server"):
		event = "stats"
	case strings.Contains(msg, "acme"), strings.Contains(msg, "certificate"):
		event = "acme"
	case strings.Contains(msg, "tls"), strings.Contains(msg, "handshake"), strings.Contains(msg, "quic"):
		event = "tls"
	case strings.Contains(msg, "tcp error"), strings.Contains(msg, "udp error"):
		event = "network_error"
	case e.Level == "ERROR" || e.Level == "FATAL":
		event = "error"
	case e.Level == "WARN":
		event = "warning"
	default:
		event = "log"
	}
	if event == "log" && e.Error != "" {
		event = "error"
	}
	if e.Level == "" {
		switch {
		case event == "error" || event == "auth_fail":
			e.Level = "ERROR"
		case event == "warning":
			e.Level = "WARN"
		default:
			e.Level = "INFO"
		}
	}
	e.Event = event
	// Keep the message compact for the UI; the raw line is kept separately.
	if len(e.Message) > 2000 {
		e.Message = e.Message[:2000]
	}
}

var xrayAccessRE = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) from (\S+) (accepted|rejected) (?:tcp|udp):(\S+) \[([^\]]+)\](?: email: (\S+))?`)

func parseXrayLine(line string, fallback time.Time) (model.LogEntry, bool) {
	if !strings.Contains(line, "/") {
		return model.LogEntry{}, false
	}
	if m := xrayAccessRE.FindStringSubmatch(line); m != nil {
		e := model.LogEntry{Raw: line, TS: fallback, Logger: "xray", Message: line, Level: "INFO"}
		if t := parseXrayTime(m[1]); !t.IsZero() {
			e.TS = t
		}
		host, port := splitHostPort(m[2])
		e.ClientIP, e.Port = host, port
		targetHost, targetPort := splitHostPort(m[4])
		e.Host, e.Port = targetHost, targetPort
		e.UserID = m[6]
		finishClassification(&e)
		if strings.Contains(m[5], "API_INBOUND") {
			e.Event = "stats"
			e.Level = "DEBUG"
		} else if m[3] == "rejected" {
			e.Event = "error"
			e.Level = "WARN"
		} else {
			e.Event = "connect"
			e.Level = "INFO"
		}
		return e, true
	}
	// Xray error/startup log: 2026/10/21 16:14:51.723692 [Warning] message
	if len(line) > 26 && line[4] == '/' && line[7] == '/' && strings.Contains(line, "[") {
		prefix := line
		rest := ""
		if idx := strings.Index(line, "["); idx > 0 {
			prefix = line[:idx]
			rest = line[idx:]
		}
		e := model.LogEntry{Raw: line, TS: fallback, Logger: "xray", Message: strings.TrimSpace(rest)}
		if t := parseXrayTime(strings.TrimSpace(prefix)); !t.IsZero() {
			e.TS = t
		}
		level := ""
		if strings.HasPrefix(rest, "[") {
			if end := strings.Index(rest, "]"); end > 0 {
				level = strings.ToLower(rest[1:end])
			}
		}
		switch level {
		case "warning", "warn":
			e.Level = "WARN"
		case "error", "fatal":
			e.Level = "ERROR"
		case "debug":
			e.Level = "DEBUG"
		default:
			e.Level = "INFO"
		}
		if e.Message == "" {
			e.Message = line
		}
		finishClassification(&e)
		if strings.Contains(strings.ToLower(e.Message), "started") {
			e.Event = "start"
		}
		return e, true
	}
	return model.LogEntry{}, false
}

func parseXrayTime(s string) time.Time {
	s = strings.TrimSpace(s)
	layouts := []string{"2006/01/02 15:04:05.000000", "2006/01/02 15:04:05", "2006/01/02 15:04:05.000"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
