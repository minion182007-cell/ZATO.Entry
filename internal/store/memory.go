package store

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/passes"
	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
)

type memPlan struct {
	body    []byte
	orgID   string
	summary PlanSummary
}

// Memory — хранилище в памяти процесса. Данные теряются при перезапуске.
type Memory struct {
	mu     sync.RWMutex
	plans  map[string]memPlan
	orgs   map[string]*Org   // id → организация
	tokens map[string]string // хеш ключа → id
	feeds  map[string]string // ключ календаря → id
	staff  map[string]map[string]passes.Employee
}

// NewMemory создаёт пустое хранилище.
func NewMemory() *Memory {
	return &Memory{
		plans: map[string]memPlan{}, orgs: map[string]*Org{},
		tokens: map[string]string{}, feeds: map[string]string{},
		staff: map[string]map[string]passes.Employee{},
	}
}

func (m *Memory) SavePlan(_ context.Context, p *planner.Plan, orgID string) error {
	id, err := randomHex(12)
	if err != nil {
		return err
	}
	p.ID = id
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[id] = memPlan{body: body, orgID: orgID, summary: PlanSummary{
		ID: id, Scenario: p.Scenario, Title: p.Title,
		StartDate: p.StartDate, TargetDate: p.TargetDate, CreatedAt: time.Now().UTC(),
	}}
	return nil
}

func (m *Memory) GetPlan(_ context.Context, id string) (*planner.Plan, error) {
	m.mu.RLock()
	rec, ok := m.plans[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	var p planner.Plan
	if err := json.Unmarshal(rec.body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *Memory) ListPlans(_ context.Context, orgID string) ([]PlanSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []PlanSummary{}
	for _, rec := range m.plans {
		if rec.orgID == orgID && orgID != "" {
			out = append(out, rec.summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) PurgeAnonymousPlans(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, rec := range m.plans {
		if rec.orgID == "" && rec.summary.CreatedAt.Before(before) {
			delete(m.plans, id)
			n++
		}
	}
	return n, nil
}

func (m *Memory) CreateOrg(_ context.Context, name, city string) (*Org, string, error) {
	org, token, err := newOrg(name, city)
	if err != nil {
		return nil, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgs[org.ID] = org
	m.tokens[hashToken(token)] = org.ID
	m.feeds[org.FeedToken] = org.ID
	m.staff[org.ID] = map[string]passes.Employee{}
	cp := *org
	return &cp, token, nil
}

func (m *Memory) OrgByToken(_ context.Context, token string) (*Org, error) {
	return m.org(m.tokens, hashToken(token))
}

func (m *Memory) OrgByFeedToken(_ context.Context, feed string) (*Org, error) {
	return m.org(m.feeds, feed)
}

func (m *Memory) org(index map[string]string, key string) (*Org, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := index[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *m.orgs[id]
	return &cp, nil
}

func (m *Memory) ListStaff(_ context.Context, orgID string) ([]passes.Employee, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]passes.Employee, 0, len(m.staff[orgID]))
	for _, e := range m.staff[orgID] {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) PutStaff(_ context.Context, orgID string, staff []passes.Employee) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.staff[orgID]
	if !ok {
		return ErrNotFound
	}
	added := 0
	for _, e := range staff {
		if _, exists := cur[e.ID]; !exists {
			added++
		}
	}
	if len(cur)+added > MaxStaff {
		return ErrLimit
	}
	for _, e := range staff {
		cur[e.ID] = e
	}
	return nil
}

func (m *Memory) GetStaff(_ context.Context, orgID, code string) (*passes.Employee, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.staff[orgID][code]
	if !ok {
		return nil, ErrNotFound
	}
	return &e, nil
}

func (m *Memory) DeleteStaff(_ context.Context, orgID, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.staff[orgID][code]; !ok {
		return ErrNotFound
	}
	delete(m.staff[orgID], code)
	return nil
}

func (m *Memory) Ping(context.Context) error { return nil }
func (m *Memory) Close() error               { return nil }
