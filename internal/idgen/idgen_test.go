package idgen

import (
	"regexp"
	"testing"
)

func TestTokenAndUUIDAreRandomAndValid(t *testing.T) {
	token := Token(16)
	if len(token) != 32 {
		t.Fatalf("Token(16) length = %d, want 32", len(token))
	}
	if token == Token(16) {
		t.Fatal("Token() returned a duplicate")
	}
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	uuid := UUID()
	if !uuidPattern.MatchString(uuid) {
		t.Fatalf("UUID() = %q, want RFC 4122 v4", uuid)
	}
	if uuid == UUID() {
		t.Fatal("UUID() returned a duplicate")
	}
}
