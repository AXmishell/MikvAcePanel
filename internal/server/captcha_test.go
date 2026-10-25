package server

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCaptchaGlyphsCoverAlphabet(t *testing.T) {
	for _, ch := range captchaAlphabet {
		glyph, ok := captchaGlyphs[ch]
		if !ok {
			t.Fatalf("no glyph for %q", ch)
		}
		for row, line := range glyph {
			if len(line) != 5 {
				t.Fatalf("glyph %q row %d = %q, want 5 columns", ch, row, line)
			}
			if strings.Trim(line, "01") != "" {
				t.Fatalf("glyph %q row %d = %q, want only 0/1", ch, row, line)
			}
		}
	}
}

func TestCaptchaImageIsSVGDataURI(t *testing.T) {
	const prefix = "data:image/svg+xml;base64,"
	image := captchaImage("AB3DE")
	if !strings.HasPrefix(image, prefix) {
		t.Fatalf("image = %.40q", image)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, prefix))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "<svg") || !strings.Contains(string(raw), "</svg>") {
		t.Fatalf("decoded captcha = %q", raw)
	}
	if strings.Contains(string(raw), "<text") {
		t.Fatal("captcha must not expose the answer as text")
	}
}

func TestRandomCaptchaCode(t *testing.T) {
	code, err := randomCaptchaCode(captchaLength)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != captchaLength {
		t.Fatalf("code = %q", code)
	}
	for _, ch := range []byte(code) {
		if !strings.ContainsRune(string(captchaAlphabet), rune(ch)) {
			t.Fatalf("code %q contains %q outside the alphabet", code, ch)
		}
	}
}
