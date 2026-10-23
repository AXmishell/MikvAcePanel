package version

import "testing"

func TestString(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })

	cases := []struct {
		version string
		commit  string
		want    string
	}{
		{"aba90dd-dirty", "aba90dd", "aba90dd-dirty"},
		{"v1.2.3", "abcdef1234567890", "v1.2.3+abcdef12"},
		{"v1.2.3-5-gabcdef1", "abcdef1", "v1.2.3-5-gabcdef1"},
		{"dev", "unknown", "dev"},
		{"dev", "", "dev"},
		{"", "abcdef1", "dev+abcdef1"},
		{"v1.0.0+abcdef12", "abcdef123456", "v1.0.0+abcdef12"},
	}
	for _, tc := range cases {
		Version, Commit = tc.version, tc.commit
		if got := String(); got != tc.want {
			t.Errorf("String() with version=%q commit=%q = %q, want %q", tc.version, tc.commit, got, tc.want)
		}
	}
}
