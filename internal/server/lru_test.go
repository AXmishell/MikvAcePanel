package server

import "testing"

func TestStringLRUEvictsLeastRecentlyUsed(t *testing.T) {
	c := newStringLRU(2)
	c.Put("a", "1")
	c.Put("b", "2")
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("get a = %q, %v", v, ok)
	}
	// "a" was just used, so inserting "c" must evict "b".
	c.Put("c", "3")
	if _, ok := c.Get("b"); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("recent entry lost: %q, %v", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != "3" {
		t.Fatalf("new entry missing: %q, %v", v, ok)
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("len = %d", got)
	}
}

func TestStringLRUUpdatesInPlace(t *testing.T) {
	c := newStringLRU(2)
	c.Put("a", "1")
	c.Put("a", "11")
	if v, ok := c.Get("a"); !ok || v != "11" {
		t.Fatalf("updated value = %q, %v", v, ok)
	}
	if got := c.Len(); got != 1 {
		t.Fatalf("len = %d", got)
	}
}

func TestStringLRUCapacityFloor(t *testing.T) {
	c := newStringLRU(0)
	c.Put("k", "v")
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Fatalf("value = %q, %v", v, ok)
	}
	if got := c.Len(); got != 1 {
		t.Fatalf("len = %d", got)
	}
}
