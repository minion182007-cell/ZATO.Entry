// Package planner превращает анкету в дорожную карту: отбирает шаги
// сценария по ответам, применяет экспертные сроки и считает даты в
// трёх вариантах — базовом, раннем и позднем.
package planner

import (
	"fmt"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/engine"
	"github.com/minion182007-cell/ZATO.Entry/internal/profile"
)

// Request — запрос на построение плана.
type Request struct {
	City      string         `json:"city"`
	Scenario  string         `json:"scenario"`
	StartDate string         `json:"start_date"` // ГГГГ-ММ-ДД; пусто — сегодня
	Answers   map[string]any `json:"answers"`
	// Overrides — сроки шагов, заданные экспертом (id шага → дни).
	Overrides map[string]int `json:"overrides"`
}

// Plan — дорожная карта въезда.
type Plan struct {
	ID        string         `json:"id,omitempty"`
	City      string         `json:"city"`
	Scenario  string         `json:"scenario"`
	Title     string         `json:"title"`
	StartDate string         `json:"start_date"`
	Answers   map[string]any `json:"answers"`
	Overrides map[string]int `json:"overrides,omitempty"`

	// TargetDate — дата итоговой вехи при базовых сроках.
	TargetDate string `json:"target_date"`
	// Earliest/Latest — вилка при минимальных и максимальных сроках.
	Earliest  string `json:"earliest_date"`
	Latest    string `json:"latest_date"`
	TotalDays int    `json:"total_days"`

	FirstAction  string   `json:"first_action"`
	CriticalPath []string `json:"critical_path"`
	Steps        []Step   `json:"steps"`
	// Assumptions — сколько шагов опирается на неподтверждённые сроки.
	Assumptions int      `json:"assumptions"`
	Limits      []string `json:"limits"`
	Disclaimer  string   `json:"disclaimer"`
}

// Step — шаг плана с датами.
type Step struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	OwnerTitle   string   `json:"owner_title,omitempty"`
	Days         int      `json:"days"`
	MinDays      int      `json:"min_days"`
	MaxDays      int      `json:"max_days"`
	Calendar     string   `json:"calendar"`
	DependsOn    []string `json:"depends_on"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	SlackDays    int      `json:"slack_days"`
	Critical     bool     `json:"critical"`
	Assumption   bool     `json:"assumption"`
	Overridden   bool     `json:"overridden"`
	Adjustable   bool     `json:"adjustable"`
	RefusalPoint bool     `json:"refusal_point"`
	Note         string   `json:"note,omitempty"`
}

const layout = "2006-01-02"

// Build строит план по профилю города.
func Build(p *profile.Profile, req Request, today time.Time) (*Plan, error) {
	sc, ok := p.Scenario(req.Scenario)
	if !ok {
		return nil, fmt.Errorf("неизвестный сценарий %q", req.Scenario)
	}
	start := engine.Day(today)
	if req.StartDate != "" {
		t, err := time.Parse(layout, req.StartDate)
		if err != nil {
			return nil, fmt.Errorf("start_date: ожидается дата в формате ГГГГ-ММ-ДД")
		}
		start = t
	}

	answers := map[string]any{}
	for _, q := range sc.Questions {
		answers[q.ID] = q.Default
		if v, ok := req.Answers[q.ID]; ok && v != nil {
			answers[q.ID] = v
		}
	}

	defs := map[string]profile.StepDef{}
	included := map[string]bool{}
	for _, st := range sc.Steps {
		defs[st.ID] = st
		included[st.ID] = matches(st.When, answers)
	}
	for id, days := range req.Overrides {
		if _, ok := defs[id]; !ok {
			return nil, fmt.Errorf("overrides: неизвестный шаг %q", id)
		}
		if days < 0 || days > 3650 {
			return nil, fmt.Errorf("overrides: недопустимый срок шага %q", id)
		}
	}

	// Зависимость от исключённого шага заменяется его зависимостями.
	var resolve func(id string, seen map[string]bool) []string
	resolve = func(id string, seen map[string]bool) []string {
		var out []string
		for _, dep := range defs[id].DependsOn {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			if included[dep] {
				out = append(out, dep)
			} else {
				out = append(out, resolve(dep, seen)...)
			}
		}
		return out
	}

	var base, min, max []engine.Step
	steps := map[string]*Step{}
	owners := map[string]string{"applicant": "Заявитель"}
	for _, a := range p.Authorities {
		owners[a.ID] = a.Title
	}
	for _, def := range sc.Steps {
		if !included[def.ID] {
			continue
		}
		deps := resolve(def.ID, map[string]bool{})
		if deps == nil {
			deps = []string{}
		}
		s := &Step{
			ID: def.ID, Title: def.Title, Description: def.Description,
			Owner: def.Owner, OwnerTitle: owners[def.Owner],
			Days: def.Days, MinDays: def.Days, MaxDays: def.Days,
			Calendar: string(def.Calendar), DependsOn: deps,
			Assumption: def.Assumption, Adjustable: def.Adjustable,
			RefusalPoint: def.RefusalPoint, Note: def.Note,
		}
		if def.MinDays != nil {
			s.MinDays = *def.MinDays
		}
		if def.MaxDays != nil {
			s.MaxDays = *def.MaxDays
		}
		// Срок, заданный экспертом, фиксирует все три варианта.
		if days, ok := req.Overrides[def.ID]; ok {
			s.Days, s.MinDays, s.MaxDays, s.Overridden = days, days, days, true
		}
		steps[def.ID] = s
		mk := func(days int) engine.Step {
			return engine.Step{ID: def.ID, Days: days, Calendar: def.Calendar, DependsOn: deps}
		}
		base = append(base, mk(s.Days))
		min = append(min, mk(s.MinDays))
		max = append(max, mk(s.MaxDays))
	}

	wd := p.Workdays()
	sched, err := engine.Compute(base, start, wd)
	if err != nil {
		return nil, err
	}
	early, err := engine.Compute(min, start, wd)
	if err != nil {
		return nil, err
	}
	late, err := engine.Compute(max, start, wd)
	if err != nil {
		return nil, err
	}

	goal, ok := sched.Step(sc.Goal)
	if !ok {
		return nil, fmt.Errorf("итоговая веха %q исключена условиями", sc.Goal)
	}
	goalEarly, _ := early.Step(sc.Goal)
	goalLate, _ := late.Step(sc.Goal)

	plan := &Plan{
		City: p.ID, Scenario: sc.ID, Title: sc.Title,
		StartDate: start.Format(layout), Answers: answers, Overrides: req.Overrides,
		TargetDate:   goal.End.Format(layout),
		Earliest:     goalEarly.End.Format(layout),
		Latest:       goalLate.End.Format(layout),
		TotalDays:    int(goal.End.Sub(start).Hours() / 24),
		CriticalPath: sched.CriticalPath,
		Limits:       p.Limits, Disclaimer: p.Disclaimer,
	}
	if included[sc.FirstAction] {
		plan.FirstAction = sc.FirstAction
	}
	for _, s := range sched.Steps {
		st := steps[s.ID]
		st.Start, st.End = s.Start.Format(layout), s.End.Format(layout)
		st.SlackDays, st.Critical = s.SlackDays, s.Critical
		if st.Assumption && !st.Overridden {
			plan.Assumptions++
		}
		plan.Steps = append(plan.Steps, *st)
	}
	return plan, nil
}

func matches(when map[string]any, answers map[string]any) bool {
	for q, want := range when {
		if fmt.Sprint(answers[q]) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}
