package engine

import (
	"strings"
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestComputeCriticalPathAndSlack(t *testing.T) {
	// 2026-11-16 — понедельник.
	steps := []Step{
		{ID: "petition", Days: 3, Calendar: WorkingDays},
		{ID: "check", Days: 75, Calendar: CalendarDays, DependsOn: []string{"petition"}},
		{ID: "premises", Days: 21, Calendar: CalendarDays},
		{ID: "open", Days: 0, DependsOn: []string{"check", "premises"}},
	}
	s, err := Compute(steps, d("2026-11-16"), NewWorkdays(nil))
	if err != nil {
		t.Fatal(err)
	}
	pet, _ := s.Step("petition")
	if got := pet.End.Format(dateLayout); got != "2026-11-18" {
		t.Errorf("petition end = %s", got)
	}
	chk, _ := s.Step("check")
	if got := chk.Start.Format(dateLayout); got != "2026-11-19" {
		t.Errorf("check start = %s", got)
	}
	// 19.11 + 75 дней = 02.02.2027 (первый день после), конец — 01.02.
	if got := chk.End.Format(dateLayout); got != "2027-02-01" {
		t.Errorf("check end = %s", got)
	}
	open, _ := s.Step("open")
	if got := open.Start.Format(dateLayout); got != "2027-02-02" {
		t.Errorf("open = %s", got)
	}
	if got := strings.Join(s.CriticalPath, ","); got != "petition,check,open" {
		t.Errorf("critical path = %s", got)
	}
	prem, _ := s.Step("premises")
	if prem.Critical || prem.SlackDays != 78-21 {
		t.Errorf("premises critical=%v slack=%d", prem.Critical, prem.SlackDays)
	}
	if chk.SlackDays != 0 {
		t.Errorf("check slack = %d", chk.SlackDays)
	}
}

func TestWorkingDaysSkipWeekendsAndHolidays(t *testing.T) {
	wd := NewWorkdays([]time.Time{d("2026-11-04")})
	// Пятница 30.10 + 5 рабочих дней, 04.11 — праздник.
	s, err := Compute([]Step{{ID: "a", Days: 5, Calendar: WorkingDays}}, d("2026-10-30"), wd)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Finish.Format(dateLayout); got != "2026-11-06" {
		t.Errorf("finish = %s", got)
	}
	// Старт в субботу переносится на понедельник.
	s, _ = Compute([]Step{{ID: "a", Days: 1, Calendar: WorkingDays}}, d("2026-11-14"), wd)
	if got := s.Steps[0].Start.Format(dateLayout); got != "2026-11-16" {
		t.Errorf("start = %s", got)
	}
}

func TestComputeErrors(t *testing.T) {
	cases := map[string][]Step{
		"цикл":        {{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}}},
		"неизвестный": {{ID: "a", DependsOn: []string{"x"}}},
		"дубль":       {{ID: "a"}, {ID: "a"}},
	}
	for name, steps := range cases {
		if _, err := Compute(steps, d("2026-11-16"), NewWorkdays(nil)); err == nil {
			t.Errorf("%s: ожидалась ошибка", name)
		}
	}
}
