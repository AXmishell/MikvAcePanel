package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestDecodeJSONRejectsTrailingData(t *testing.T) {
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "single value", body: `{"name":"node"}`, ok: true},
		{name: "trailing value", body: `{"name":"node"} {"name":"other"}`, ok: false},
		{name: "trailing garbage", body: `{"name":"node"}x`, ok: false},
		{name: "empty", body: ``, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var dst map[string]any
			err := decodeJSON(r, &dst)
			if tc.ok && err != nil {
				t.Fatalf("decodeJSON() error = %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("decodeJSON() accepted invalid body")
			}
		})
	}
}

func TestPathIDRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		raw  string
		want int64
		ok   bool
	}{
		{raw: "42", want: 42, ok: true},
		{raw: "1abc", ok: false},
		{raw: "0", ok: false},
		{raw: "-1", ok: false},
		{raw: "", ok: false},
		{raw: " 7", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			routeCtx := chi.NewRouteContext()
			routeCtx.URLParams.Add("id", tc.raw)
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
			got, err := pathID(r)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("pathID() = %d, %v; want %d, nil", got, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("pathID(%q) accepted value %d", tc.raw, got)
			}
		})
	}
}

func TestWriteJSONEncodingFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, make(chan int))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["error"] == "" {
		t.Fatalf("missing error message: %q", rec.Body.String())
	}
}
