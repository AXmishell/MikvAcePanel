package server

import (
	"strings"
	"testing"
)

func TestNormalizeNodeListen(t *testing.T) {
	cases := []struct {
		name      string
		listen    string
		port      int
		want      string
		wantPort  int
		wantError string
	}{
		{name: "empty uses node port", listen: "", port: 15443, want: "", wantPort: 15443},
		{name: "empty defaults to 443", listen: "", port: 0, want: "", wantPort: 443},
		{name: "colon port", listen: ":15443", port: 15443, want: ":15443", wantPort: 15443},
		{name: "bare port", listen: "15443", port: 15443, want: ":15443", wantPort: 15443},
		{name: "bare port sets node port", listen: "15443", port: 0, want: ":15443", wantPort: 15443},
		{name: "host and port", listen: "0.0.0.0:15443", port: 15443, want: "0.0.0.0:15443", wantPort: 15443},
		{name: "ipv6 host and port", listen: "[::]:15443", port: 15443, want: "[::]:15443", wantPort: 15443},
		{name: "bare host gets node port", listen: "0.0.0.0", port: 15443, want: "0.0.0.0:15443", wantPort: 15443},
		{name: "bare ipv6 host gets node port", listen: "::", port: 15443, want: "[::]:15443", wantPort: 15443},
		{name: "bare hostname gets node port", listen: "localhost", port: 15443, want: "localhost:15443", wantPort: 15443},
		{name: "bare host defaults port", listen: "0.0.0.0", port: 0, want: "0.0.0.0:443", wantPort: 443},
		{
			name: "listen and node port conflict", listen: ":15443", port: 443,
			wantError: "conflicts with node port",
		},
		{
			name: "bare port and node port conflict", listen: "15443", port: 443,
			wantError: "conflicts with node port",
		},
		{name: "invalid listen port", listen: "0.0.0.0:70000", wantError: "invalid port"},
		{name: "invalid node port", listen: "", port: 70000, wantError: "port must be between"},
		{name: "invalid host", listen: "not a host:15443", wantError: "invalid host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listen, port, err := normalizeNodeListen(tc.listen, tc.port)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if listen != tc.want || port != tc.wantPort {
				t.Fatalf("normalizeNodeListen(%q, %d) = %q, %d; want %q, %d",
					tc.listen, tc.port, listen, port, tc.want, tc.wantPort)
			}
		})
	}
}

func TestNodeFromPayloadNormalizesListen(t *testing.T) {
	s := &Server{}
	cases := []struct {
		name       string
		listen     string
		port       int
		wantListen string
		wantPort   int
	}{
		{name: "bare host", listen: "0.0.0.0", port: 15443, wantListen: "0.0.0.0:15443", wantPort: 15443},
		{name: "bare port", listen: "15443", port: 15443, wantListen: ":15443", wantPort: 15443},
		{name: "empty listen", listen: "", port: 15443, wantListen: "", wantPort: 15443},
		{name: "listen sets port", listen: ":15443", port: 0, wantListen: ":15443", wantPort: 15443},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := s.nodeFromPayload(&nodePayload{
				Name: "edge", Listen: tc.listen, Port: tc.port, TLSMode: "selfsigned",
			}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n.Listen != tc.wantListen || n.Port != tc.wantPort {
				t.Fatalf("node listen/port = %q/%d, want %q/%d",
					n.Listen, n.Port, tc.wantListen, tc.wantPort)
			}
		})
	}
}

func TestNodeFromPayloadRejectsListenPortMismatch(t *testing.T) {
	s := &Server{}
	_, err := s.nodeFromPayload(&nodePayload{
		Name: "edge", Listen: ":15443", Port: 443, TLSMode: "selfsigned",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "conflicts with node port") {
		t.Fatalf("expected listen/port conflict, got %v", err)
	}
}
