package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

const (
	// captchaTTL is how long a generated image can be submitted.
	captchaTTL = 5 * time.Minute
	// loginCaptchaWindow is how long a client must keep solving captchas
	// after a failed administrator login.
	loginCaptchaWindow = 30 * time.Minute
	captchaLength      = 5
	captchaMaxEntries  = 4096
)

// captchaAlphabet omits characters that are easy to confuse: I, O, 0 and 1.
var captchaAlphabet = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")

var captchaColors = []string{"#ff6b3d", "#e6b64c", "#93b8a5", "#f2efe7", "#ff8259"}

// captchaResponse is the public part of a captcha challenge.
type captchaResponse struct {
	ID        string    `json:"id"`
	Image     string    `json:"image"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type captchaEntry struct {
	answer  string
	expires time.Time
}

// issueLoginCaptcha creates a one-time challenge. Challenges live in Redis when
// it is configured so a load-balanced panel keeps working, with an in-memory
// fallback for single-instance deployments.
func (s *Server) issueLoginCaptcha(ctx context.Context) (captchaResponse, error) {
	code, err := randomCaptchaCode(captchaLength)
	if err != nil {
		return captchaResponse{}, err
	}
	id := store.RandomToken(16)
	expires := time.Now().Add(captchaTTL).UTC()
	key := "login:captcha:" + id
	if s.redis != nil {
		err := s.redis.Set(ctx, key, code, captchaTTL)
		if err == nil {
			return captchaResponse{ID: id, Image: captchaImage(code), ExpiresAt: expires}, nil
		}
		s.logRedisError("redis captcha store failed, falling back to memory", err)
	}
	s.captchaMu.Lock()
	s.pruneCaptchasLocked(time.Now())
	s.captchas[id] = captchaEntry{answer: code, expires: expires}
	s.captchaMu.Unlock()
	return captchaResponse{ID: id, Image: captchaImage(code), ExpiresAt: expires}, nil
}

// verifyLoginCaptcha consumes a challenge, so an image can only be submitted
// once. Expired or unknown challenges fail closed.
func (s *Server) verifyLoginCaptcha(ctx context.Context, id, answer string) bool {
	id = strings.TrimSpace(id)
	answer = strings.ToUpper(strings.TrimSpace(answer))
	if id == "" || answer == "" {
		return false
	}
	key := "login:captcha:" + id
	if s.redis != nil {
		stored, ok, err := s.redis.Get(ctx, key)
		if err == nil {
			_ = s.redis.Delete(ctx, key)
			return ok && subtle.ConstantTimeCompare([]byte(stored), []byte(answer)) == 1
		}
		s.logRedisError("redis captcha lookup failed, falling back to memory", err)
	}
	s.captchaMu.Lock()
	entry, ok := s.captchas[id]
	delete(s.captchas, id)
	s.captchaMu.Unlock()
	if !ok || time.Now().After(entry.expires) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(entry.answer), []byte(answer)) == 1
}

// requireLoginCaptcha marks a client as needing a captcha. The flag survives
// failed attempts for loginCaptchaWindow and is cleared by a successful login.
func (s *Server) requireLoginCaptcha(ctx context.Context, scope, ip string) {
	key := loginCaptchaGateKey(scope, ip)
	if s.redis != nil {
		err := s.redis.Set(ctx, key, "1", loginCaptchaWindow)
		if err == nil {
			return
		}
		s.logRedisError("redis captcha gate failed, falling back to memory", err)
	}
	s.captchaMu.Lock()
	s.pruneCaptchasLocked(time.Now())
	s.captchaGate[key] = time.Now().Add(loginCaptchaWindow)
	s.captchaMu.Unlock()
}

func (s *Server) loginCaptchaRequired(ctx context.Context, scope, ip string) bool {
	key := loginCaptchaGateKey(scope, ip)
	if s.redis != nil {
		v, ok, err := s.redis.Get(ctx, key)
		if err == nil {
			return ok && v == "1"
		}
		s.logRedisError("redis captcha gate lookup failed, falling back to memory", err)
	}
	s.captchaMu.Lock()
	defer s.captchaMu.Unlock()
	until, ok := s.captchaGate[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(s.captchaGate, key)
		return false
	}
	return true
}

func (s *Server) clearLoginCaptcha(ctx context.Context, scope, ip string) {
	key := loginCaptchaGateKey(scope, ip)
	if s.redis != nil {
		if err := s.redis.Delete(ctx, key); err != nil {
			s.logRedisError("redis captcha gate clear failed", err)
		}
	}
	s.captchaMu.Lock()
	delete(s.captchaGate, key)
	s.captchaMu.Unlock()
}

func loginCaptchaGateKey(scope, ip string) string {
	return "login:captcha-required:" + scope + ":" + ip
}

// pruneCaptchasLocked bounds both maps. Callers must hold captchaMu.
func (s *Server) pruneCaptchasLocked(nowT time.Time) {
	for id, entry := range s.captchas {
		if nowT.After(entry.expires) {
			delete(s.captchas, id)
		}
	}
	for key, until := range s.captchaGate {
		if nowT.After(until) {
			delete(s.captchaGate, key)
		}
	}
	for len(s.captchas) > captchaMaxEntries {
		for id := range s.captchas {
			delete(s.captchas, id)
			break
		}
	}
	for len(s.captchaGate) > captchaMaxEntries {
		for key := range s.captchaGate {
			delete(s.captchaGate, key)
			break
		}
	}
}

// writeCaptchaChallenge writes a login error and, when captcha is required,
// includes a fresh challenge so the client can retry without another round trip.
func (s *Server) writeCaptchaChallenge(w http.ResponseWriter, r *http.Request, status int, msg string) {
	payload := map[string]any{"error": msg}
	captcha, err := s.issueLoginCaptcha(r.Context())
	if err != nil {
		s.log.Error("generate login captcha", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create captcha")
		return
	}
	payload["captchaRequired"] = true
	payload["captcha"] = captcha
	writeJSON(w, status, payload)
}

func (s *Server) handleLoginCaptcha(w http.ResponseWriter, r *http.Request) {
	captcha, err := s.issueLoginCaptcha(r.Context())
	if err != nil {
		s.log.Error("generate login captcha", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create captcha")
		return
	}
	writeJSON(w, http.StatusOK, captcha)
}

func randomCaptchaCode(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(captchaAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("random captcha: %w", err)
		}
		out[i] = captchaAlphabet[idx.Int64()]
	}
	return string(out), nil
}

func randomInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

// captchaImage renders the code as a self-contained SVG data URI. Glyphs are
// drawn as rectangles instead of <text> so the answer is not readable from the
// image markup, and light noise lines/dots break up simple OCR.
func captchaImage(code string) string {
	const width, height = 168, 60
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="captcha">`,
		width, height, width, height)
	b.WriteString(`<rect width="100%" height="100%" fill="#191917"/>`)
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, `<path d="M%d %d L%d %d" stroke="#3d3d36" stroke-width="1.2" opacity=".9"/>`,
			randomInt(width), randomInt(height), randomInt(width), randomInt(height))
	}
	for i, ch := range []byte(code) {
		glyph, ok := captchaGlyphs[ch]
		if !ok {
			continue
		}
		x := 10 + i*30 + randomInt(5) - 2
		y := 12 + randomInt(7) - 3
		rotate := randomInt(15) - 7
		color := captchaColors[randomInt(len(captchaColors))]
		fmt.Fprintf(&b, `<g transform="rotate(%d %d %d)" fill="%s">`, rotate, x+10, y+14, color)
		for row := 0; row < 7; row++ {
			for col := 0; col < 5; col++ {
				if glyph[row][col] != '1' {
					continue
				}
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="4" height="4"/>`, x+col*4, y+row*4)
			}
		}
		b.WriteString(`</g>`)
	}
	for i := 0; i < 22; i++ {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="%d" fill="#77736a" opacity=".55"/>`,
			randomInt(width), randomInt(height), 1+randomInt(2))
	}
	b.WriteString(`</svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(b.String()))
}

// captchaGlyphs is a 5x7 bitmap font for the captcha alphabet.
var captchaGlyphs = map[byte][7]string{
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01110", "10001", "10000", "10000", "10000", "10001", "01110"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01110", "10001", "10000", "10111", "10001", "10001", "01111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'J': {"00111", "00010", "00010", "00010", "00010", "10010", "01100"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "11011", "10001"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11111", "00010", "00100", "00010", "00001", "10001", "01110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6': {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
}
