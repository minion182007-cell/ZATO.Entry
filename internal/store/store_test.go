package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/passes"
	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
	"github.com/minion182007-cell/ZATO.Entry/internal/store"
)

func TestMemory(t *testing.T) {
	suite(t, store.NewMemory())
}

// Тест на живой базе: ZATO_TEST_DATABASE_URL=postgres://… go test ./internal/store
func TestPostgres(t *testing.T) {
	url := os.Getenv("ZATO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ZATO_TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	pg, err := store.OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	// Повторное открытие не должно заново накатывать миграции.
	again, err := store.OpenPostgres(ctx, url)
	if err != nil {
		t.Fatalf("повторные миграции: %v", err)
	}
	again.Close()
	suite(t, pg)
}

func suite(t *testing.T, s store.Store) {
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	org, token, err := s.CreateOrg(ctx, "ООО «Пример»", "sarov")
	if err != nil {
		t.Fatal(err)
	}
	other, otherToken, _ := s.CreateOrg(ctx, "Другая", "sarov")
	if token == otherToken || org.FeedToken == other.FeedToken || org.ID == other.ID {
		t.Fatal("ключи организаций должны различаться")
	}

	t.Run("организации", func(t *testing.T) {
		got, err := s.OrgByToken(ctx, token)
		if err != nil || got.ID != org.ID || got.Name != "ООО «Пример»" {
			t.Fatalf("OrgByToken: %+v %v", got, err)
		}
		if got, err := s.OrgByFeedToken(ctx, org.FeedToken); err != nil || got.ID != org.ID {
			t.Fatalf("OrgByFeedToken: %+v %v", got, err)
		}
		if _, err := s.OrgByToken(ctx, "zk_wrong"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("чужой ключ: %v", err)
		}
		// Ключ календаря не даёт доступа на запись.
		if _, err := s.OrgByToken(ctx, org.FeedToken); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ключ календаря принят как ключ доступа: %v", err)
		}
	})

	t.Run("планы", func(t *testing.T) {
		mk := func() *planner.Plan {
			return &planner.Plan{
				City: "sarov", Scenario: "work", Title: "Работа",
				StartDate: "2026-11-16", TargetDate: "2027-02-22",
				Answers: map[string]any{"staff_count": float64(5)},
				Steps:   []planner.Step{{ID: "w_check", Title: "Спецпроверка", Days: 75, DependsOn: []string{}}},
			}
		}
		owned, anon := mk(), mk()
		if err := s.SavePlan(ctx, owned, org.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.SavePlan(ctx, anon, ""); err != nil {
			t.Fatal(err)
		}
		if owned.ID == "" || owned.ID == anon.ID {
			t.Fatal("план должен получить уникальный id")
		}
		got, err := s.GetPlan(ctx, owned.ID)
		if err != nil || got.TargetDate != "2027-02-22" || len(got.Steps) != 1 || got.Steps[0].Days != 75 {
			t.Fatalf("GetPlan: %+v %v", got, err)
		}
		if _, err := s.GetPlan(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("нет плана: %v", err)
		}
		list, err := s.ListPlans(ctx, org.ID)
		if err != nil || len(list) != 1 || list[0].ID != owned.ID || list[0].TargetDate != "2027-02-22" {
			t.Fatalf("ListPlans: %+v %v", list, err)
		}
		if list, _ := s.ListPlans(ctx, other.ID); len(list) != 0 {
			t.Errorf("чужие планы видны: %+v", list)
		}
		// Очистка трогает только планы без владельца.
		if n, err := s.PurgeAnonymousPlans(ctx, time.Now().Add(-time.Hour)); err != nil || n != 0 {
			t.Errorf("очистка свежих: %d %v", n, err)
		}
		if _, err := s.PurgeAnonymousPlans(ctx, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetPlan(ctx, anon.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("план без владельца не удалён: %v", err)
		}
		if _, err := s.GetPlan(ctx, owned.ID); err != nil {
			t.Errorf("план организации удалён: %v", err)
		}
	})

	t.Run("штат", func(t *testing.T) {
		err := s.PutStaff(ctx, org.ID, []passes.Employee{
			{ID: "С-002", Name: "Монтажник", PassExpires: "2027-01-04"},
			{ID: "С-001", PassExpires: "2027-06-01"},
		})
		if err != nil {
			t.Fatal(err)
		}
		// Повторная запись обновляет, а не дублирует.
		if err := s.PutStaff(ctx, org.ID, []passes.Employee{{ID: "С-001", Name: "Сварщик", PassExpires: "2028-06-01"}}); err != nil {
			t.Fatal(err)
		}
		list, err := s.ListStaff(ctx, org.ID)
		if err != nil || len(list) != 2 || list[0].ID != "С-001" || list[0].PassExpires != "2028-06-01" || list[0].Name != "Сварщик" {
			t.Fatalf("ListStaff: %+v %v", list, err)
		}
		if e, err := s.GetStaff(ctx, org.ID, "С-002"); err != nil || e.PassExpires != "2027-01-04" {
			t.Fatalf("GetStaff: %+v %v", e, err)
		}
		if list, _ := s.ListStaff(ctx, other.ID); len(list) != 0 {
			t.Errorf("чужой штат виден: %+v", list)
		}
		if _, err := s.GetStaff(ctx, other.ID, "С-001"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("чужой сотрудник доступен: %v", err)
		}
		if err := s.DeleteStaff(ctx, other.ID, "С-001"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("удаление чужого: %v", err)
		}
		if err := s.DeleteStaff(ctx, org.ID, "С-001"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteStaff(ctx, org.ID, "С-001"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("повторное удаление: %v", err)
		}
		if err := s.PutStaff(ctx, "missing", []passes.Employee{{ID: "x", PassExpires: "2027-01-01"}}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("штат несуществующей организации: %v", err)
		}
	})

	t.Run("лимит штата", func(t *testing.T) {
		big := make([]passes.Employee, store.MaxStaff+1)
		for i := range big {
			big[i] = passes.Employee{ID: fmt.Sprintf("L-%05d", i), PassExpires: "2027-01-01"}
		}
		if err := s.PutStaff(ctx, other.ID, big); !errors.Is(err, store.ErrLimit) {
			t.Fatalf("ожидался ErrLimit: %v", err)
		}
		if list, _ := s.ListStaff(ctx, other.ID); len(list) != 0 {
			t.Errorf("при превышении лимита ничего не должно записаться, записано %d", len(list))
		}
	})
}
