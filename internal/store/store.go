// Package store хранит построенные планы. Интерфейс позволяет заменить
// хранилище в памяти на PostgreSQL без изменения API.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
)

// Plans — хранилище планов.
type Plans interface {
	Save(p *planner.Plan) (string, error)
	Get(id string) (*planner.Plan, bool)
}

// Memory — хранилище в памяти процесса с ограничением по числу планов.
type Memory struct {
	mu    sync.RWMutex
	plans map[string]*planner.Plan
	order []string
	limit int
}

// NewMemory создаёт хранилище; при превышении limit вытесняются старые планы.
func NewMemory(limit int) *Memory {
	return &Memory{plans: map[string]*planner.Plan{}, limit: limit}
}

// Save сохраняет план и присваивает ему идентификатор.
func (m *Memory) Save(p *planner.Plan) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	m.mu.Lock()
	defer m.mu.Unlock()
	p.ID = id
	m.plans[id] = p
	m.order = append(m.order, id)
	for len(m.order) > m.limit {
		delete(m.plans, m.order[0])
		m.order = m.order[1:]
	}
	return id, nil
}

// Get возвращает план по идентификатору.
func (m *Memory) Get(id string) (*planner.Plan, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.plans[id]
	return p, ok
}
