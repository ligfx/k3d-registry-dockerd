package main

import (
	"sync"
)

// KeyedMutexPool exposes locks based on a passed in key, so that
// only one goroutine may be working on any specific key at one time,
// while other goroutines can work on other keys.

type KeyedMutexPoolEntry struct {
	count int
	cond  *sync.Cond
	mu    sync.Mutex
}

type KeyedMutexPool struct {
	mu      sync.Mutex
	entries map[string]*KeyedMutexPoolEntry
}

func (pool *KeyedMutexPool) Do(key string, f func() (any, error)) (any, error) {
	pool.mu.Lock()
	if pool.entries == nil {
		pool.entries = make(map[string]*KeyedMutexPoolEntry)
	}
	c, ok := pool.entries[key]
	if !ok {
		c = new(KeyedMutexPoolEntry)
		c.cond = sync.NewCond(&c.mu)
		pool.entries[key] = c
	}
	c.count++
	pool.mu.Unlock()
	c.mu.Lock()

	result, err := f()

	pool.mu.Lock()
	c.count--
	if c.count == 0 {
		delete(pool.entries, key)
	}
	pool.mu.Unlock()
	c.mu.Unlock()

	return result, err
}
