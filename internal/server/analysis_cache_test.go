package server

import "testing"

func TestNodeScopeKeySeparatesNilAndEmpty(t *testing.T) {
	if nodeScopeKey(nil) == nodeScopeKey([]int64{}) {
		t.Fatal("unrestricted and empty node scopes must not share a cache key")
	}
	if nodeScopeKey([]int64{1, 2}) == nodeScopeKey([]int64{1}) {
		t.Fatal("different node scopes must not share a cache key")
	}
}
