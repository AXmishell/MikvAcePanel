package model

import "testing"

func TestEffectiveListenPort(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want int
	}{
		{
			name: "node port",
			node: Node{Port: 443},
			want: 443,
		},
		{
			name: "default port",
			node: Node{},
			want: 443,
		},
		{
			name: "listen port wins",
			node: Node{Port: 443, Listen: ":15443"},
			want: 15443,
		},
		{
			name: "listen host and port",
			node: Node{Port: 443, Listen: "0.0.0.0:15443"},
			want: 15443,
		},
		{
			name: "ipv6 listen",
			node: Node{Port: 443, Listen: "[::]:8443"},
			want: 8443,
		},
		{
			name: "built-in range uses lowest port",
			node: Node{Port: 15443, Hy2: Hy2Settings{ListenPortRange: "20000-50000"}},
			want: 20000,
		},
		{
			name: "built-in multi range uses lowest start",
			node: Node{Port: 15443, Hy2: Hy2Settings{ListenPortRange: "30000-30010,20000-20005"}},
			want: 20000,
		},
		{
			name: "built-in single port",
			node: Node{Port: 15443, Hy2: Hy2Settings{ListenPortRange: "22000"}},
			want: 22000,
		},
		{
			name: "realm keeps node port",
			node: Node{Port: 15443, Hy2: Hy2Settings{Realm: &Hy2Realm{URI: "realm://public@realm.hy2.io/my-server-8f3a"}}},
			want: 15443,
		},
		{
			name: "listen range uses lowest port",
			node: Node{Port: 443, Listen: ":20000-50000"},
			want: 20000,
		},
		{
			name: "legacy bare port",
			node: Node{Port: 443, Listen: "15443"},
			want: 15443,
		},
		{
			name: "malformed listen falls back",
			node: Node{Port: 15443, Listen: "0.0.0.0"},
			want: 15443,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.EffectiveListenPort(); got != tc.want {
				t.Fatalf("EffectiveListenPort() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPublishesEndpoints(t *testing.T) {
	var h Hy2Settings
	if !h.PublishesTLS() || !h.PublishesHopping() {
		t.Fatal("missing toggles must default to enabled")
	}

	on, off := true, false
	h = Hy2Settings{IncludeTLS: &off, IncludeHopping: &on}
	if h.PublishesTLS() || !h.PublishesHopping() {
		t.Fatal("explicit endpoint toggles were not honoured")
	}
	h = Hy2Settings{IncludeTLS: &on, IncludeHopping: &off}
	if !h.PublishesTLS() || h.PublishesHopping() {
		t.Fatal("explicit endpoint toggles were not honoured")
	}
}

func TestListenHost(t *testing.T) {
	cases := []struct {
		listen string
		want   string
	}{
		{"", ""},
		{":15443", ""},
		{"0.0.0.0:15443", "0.0.0.0"},
		{"127.0.0.1:15443", "127.0.0.1"},
		{"[::]:15443", "::"},
		{"0.0.0.0", "0.0.0.0"},
		{"::", "::"},
		{"localhost", "localhost"},
		{"not a host:443", ""},
	}
	for _, tc := range cases {
		n := Node{Listen: tc.listen}
		if got := n.ListenHost(); got != tc.want {
			t.Errorf("ListenHost(%q) = %q, want %q", tc.listen, got, tc.want)
		}
	}
}
