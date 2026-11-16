package server

import (
	"container/list"
	"sync"
)

// stringLRU is a small concurrency-safe LRU cache for string values. Caches
// keyed by user-controlled input (subscription URLs, for example) must have a
// hard capacity so a long lived process cannot grow without bound.
type stringLRU struct {
	mu    sync.Mutex
	cap   int
	order *list.List
	items map[string]*list.Element
}

type stringLRUEntry struct {
	key   string
	value string
}

func newStringLRU(capacity int) *stringLRU {
	if capacity <= 0 {
		capacity = 1
	}
	return &stringLRU{
		cap:   capacity,
		order: list.New(),
		items: make(map[string]*list.Element, capacity),
	}
}

// Get returns the cached value and marks it as recently used.
func (c *stringLRU) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.order.MoveToFront(el)
	return el.Value.(*stringLRUEntry).value, true
}

// Put stores a value, evicting the least recently used entry when full.
func (c *stringLRU) Put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*stringLRUEntry).value = value
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&stringLRUEntry{key: key, value: value})
	c.items[key] = el
	for c.order.Len() > c.cap {
		last := c.order.Back()
		if last == nil {
			break
		}
		entry := last.Value.(*stringLRUEntry)
		delete(c.items, entry.key)
		c.order.Remove(last)
	}
}

// Len returns the number of cached entries.
func (c *stringLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
