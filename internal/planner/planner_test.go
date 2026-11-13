package planner_test

import (
	"testing"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
	"github.com/minion182007-cell/ZATO.Entry/profiles"
)

var today = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC) // понедельник

func build(t *testing.T, req planner.Request) *planner.Plan {
	t.Helper()
	reg, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := reg.Get("sarov")
	plan, err := planner.Build(p, req, today)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestAllScenariosProduceOrderedDates(t *testing.T) {
	for _, sc := range []string{"visit", "work", "business"} {
		plan := build(t, planner.Request{Scenario: sc})
		if !(plan.Earliest <= plan.TargetDate && plan.TargetDate <= plan.Latest) {
			t.Errorf("%s: вилка %s ≤ %s ≤ %s нарушена", sc, plan.Earliest, plan.TargetDate, plan.Latest)
		}
		if plan.FirstAction == "" || len(plan.CriticalPath) == 0 {
			t.Errorf("%s: нет первого действия или критического пути", sc)
		}
		starts := map[string]string{}
		ends := map[string]string{}
		for _, s := range plan.Steps {
			starts[s.ID], ends[s.ID] = s.Start, s.End
		}
		for _, s := range plan.Steps {
			for _, dep := range s.DependsOn {
				if s.Days > 0 && starts[s.ID] <= ends[dep] {
					t.Errorf("%s: шаг %s стартует %s, раньше конца %s (%s)", sc, s.ID, s.Start, dep, ends[dep])
				}
			}
		}
	}
}

func TestBusinessPetitionFiledInFirstWeek(t *testing.T) {
	plan := build(t, planner.Request{Scenario: "business"})
	for _, s := range plan.Steps {
		if s.ID == "b_petition" && s.End > "2026-11-20" {
			t.Errorf("ходатайства поданы %s — позже первой недели", s.End)
		}
	}
}

func TestOverrideMovesTargetDate(t *testing.T) {
	base := build(t, planner.Request{Scenario: "work"})
	fast := build(t, planner.Request{Scenario: "work", Overrides: map[string]int{"w_check": 60}})
	if fast.TargetDate >= base.TargetDate {
		t.Errorf("срок 60 дней не приблизил дату: %s vs %s", fast.TargetDate, base.TargetDate)
	}
	if fast.Earliest != fast.TargetDate || fast.Latest != fast.TargetDate {
		t.Errorf("экспертный срок должен схлопнуть вилку: %s..%s..%s", fast.Earliest, fast.TargetDate, fast.Latest)
	}
	if fast.Assumptions >= base.Assumptions {
		t.Errorf("подтверждённый срок должен уменьшить число допущений")
	}
}

func TestAnswersRebuildGraph(t *testing.T) {
	with := build(t, planner.Request{Scenario: "business"})
	without := build(t, planner.Request{Scenario: "business", Answers: map[string]any{
		"founder_nonresident": false, "needs_fitout": false,
	}})
	if len(without.Steps) != len(with.Steps)-2 {
		t.Errorf("шагов %d, ожидалось %d", len(without.Steps), len(with.Steps)-2)
	}
	for _, s := range without.Steps {
		if s.ID == "b_premises" && len(s.DependsOn) != 1 {
			t.Errorf("b_premises должен унаследовать зависимость от b_consult: %v", s.DependsOn)
		}
	}
}

func TestUnknownOverrideRejected(t *testing.T) {
	reg, _ := profiles.Load()
	p, _ := reg.Get("sarov")
	if _, err := planner.Build(p, planner.Request{Scenario: "work", Overrides: map[string]int{"nope": 1}}, today); err == nil {
		t.Error("ожидалась ошибка")
	}
}
