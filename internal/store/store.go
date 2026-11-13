// Package store — хранилище сервиса: планы, организации и их штат.
// Две реализации за одним интерфейсом: PostgreSQL для работы и память
// для разработки и тестов.
//
// Пакеты заявок с паспортными данными в хранилище не попадают: о
// сотруднике хранится только код, необязательное имя и срок пропуска.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/passes"
	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
)

// Ошибки хранилища.
var (
	ErrNotFound = errors.New("не найдено")
	ErrLimit    = errors.New("превышен лимит записей")
)

// MaxStaff — предел числа сотрудников в одной организации.
const MaxStaff = 5000

// Org — организация (кабинет): владелец штата и сохранённых планов.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	City string `json:"city"`
	// FeedToken — ключ только для чтения календаря продлений (.ics).
	FeedToken string    `json:"feed_token"`
	CreatedAt time.Time `json:"created_at"`
}

// PlanSummary — строка списка сохранённых планов.
type PlanSummary struct {
	ID         string    `json:"id"`
	Scenario   string    `json:"scenario"`
	Title      string    `json:"title"`
	StartDate  string    `json:"start_date"`
	TargetDate string    `json:"target_date"`
	CreatedAt  time.Time `json:"created_at"`
}

// Store — хранилище сервиса.
type Store interface {
	// SavePlan сохраняет план и присваивает ему идентификатор.
	// orgID пуст для плана без владельца (доступен по ссылке).
	SavePlan(ctx context.Context, p *planner.Plan, orgID string) error
	GetPlan(ctx context.Context, id string) (*planner.Plan, error)
	ListPlans(ctx context.Context, orgID string) ([]PlanSummary, error)
	// PurgeAnonymousPlans удаляет планы без владельца старше before.
	PurgeAnonymousPlans(ctx context.Context, before time.Time) (int64, error)

	// CreateOrg создаёт организацию и возвращает ключ доступа. Ключ
	// хранится только в виде хеша и повторно не выдаётся.
	CreateOrg(ctx context.Context, name, city string) (*Org, string, error)
	OrgByToken(ctx context.Context, token string) (*Org, error)
	OrgByFeedToken(ctx context.Context, feedToken string) (*Org, error)

	ListStaff(ctx context.Context, orgID string) ([]passes.Employee, error)
	// PutStaff добавляет сотрудников или обновляет существующих по коду.
	PutStaff(ctx context.Context, orgID string, staff []passes.Employee) error
	GetStaff(ctx context.Context, orgID, code string) (*passes.Employee, error)
	DeleteStaff(ctx context.Context, orgID, code string) error

	Ping(ctx context.Context) error
	Close() error
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// newOrg генерирует идентификатор, ключ доступа и ключ календаря.
func newOrg(name, city string) (*Org, string, error) {
	id, err := randomHex(8)
	if err != nil {
		return nil, "", err
	}
	secret, err := randomHex(24)
	if err != nil {
		return nil, "", err
	}
	feed, err := randomHex(24)
	if err != nil {
		return nil, "", err
	}
	return &Org{ID: id, Name: name, City: city, FeedToken: feed, CreatedAt: time.Now().UTC()}, "zk_" + secret, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
