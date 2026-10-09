// Package storage holds mappings from short code to long URL.
// This file is the in-memory implementation; Postgres will be added later
// behind the same Store interface so handlers don't care which backs them.
package storage

import (
	"errors"
	"sync"
)

var (
	ErrNotFound  = errors.New("code not found")
	ErrCodeTaken = errors.New("code already exists")
)

// Store is the storage interface the HTTP handlers code against.
// Dependency inversion: swap in Postgres later by writing another
// implementation of this interface, no handler changes.
type Store interface {
	Save(code, longURL string) error
	Get(code string) (string, error)
}

// Memory is a concurrency-safe in-memory Store for development.
// RWMutex lets many readers run concurrently; writes are exclusive.
type Memory struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMemory() *Memory {
	return &Memory{data: make(map[string]string)}
}

func (m *Memory) Save(code, longURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.data[code]; exists {
		return ErrCodeTaken
	}
	m.data[code] = longURL
	return nil
}

func (m *Memory) Get(code string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	longURL, ok := m.data[code]
	if !ok {
		return "", ErrNotFound
	}
	return longURL, nil
}
