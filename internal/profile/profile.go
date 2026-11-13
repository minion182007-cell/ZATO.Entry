// Package profile описывает профиль территории: формы, сроки, точки
// отказа и оргструктуру конкретного ЗАТО. Ядро от профиля не зависит —
// масштабирование на новый город делается конфигурацией, а не форком.
package profile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/engine"
)

// Profile — профиль города.
type Profile struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Region      string      `json:"region"`
	Disclaimer  string      `json:"disclaimer"`
	Limits      []string    `json:"limits"`
	Holidays    []string    `json:"holidays"`
	Authorities []Authority `json:"authorities"`
	Scenarios   []Scenario  `json:"scenarios"`
	PassTypes   []PassType  `json:"pass_types"`
	Renewal     Renewal     `json:"renewal"`
}

// Authority — участник оргструктуры (кто принимает решения и кто ведёт).
type Authority struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Role       string `json:"role"` // decision | service | channel
	ReportsTo  string `json:"reports_to,omitempty"`
	Assumption bool   `json:"assumption,omitempty"`
}

// Scenario — основание въезда: «Визит», «Работа», «Бизнес».
type Scenario struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	PassType    string     `json:"pass_type"`
	FirstAction string     `json:"first_action"` // id шага, с которого начинать
	Goal        string     `json:"goal"`         // id итоговой вехи
	Questions   []Question `json:"questions"`
	Steps       []StepDef  `json:"steps"`
}

// Question — вопрос анкеты.
type Question struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // bool | int | select
	Options []Option `json:"options,omitempty"`
	Default any      `json:"default"`
}

// Option — вариант ответа для select.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// StepDef — шаг дорожной карты.
type StepDef struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Owner       string              `json:"owner,omitempty"` // id из authorities или "applicant"
	Days        int                 `json:"days"`
	MinDays     *int                `json:"min_days,omitempty"`
	MaxDays     *int                `json:"max_days,omitempty"`
	Calendar    engine.CalendarKind `json:"calendar"`
	DependsOn   []string            `json:"depends_on,omitempty"`
	// When — условия включения шага: ответ анкеты должен совпасть.
	When map[string]any `json:"when,omitempty"`
	// Assumption — срок является оценочным допущением, а не нормой регламента.
	Assumption bool   `json:"assumption,omitempty"`
	Note       string `json:"note,omitempty"`
	// Adjustable — срок выносится эксперту на регулировку (ползунок).
	Adjustable bool `json:"adjustable,omitempty"`
	// RefusalPoint — точка возможного отказа.
	RefusalPoint bool `json:"refusal_point,omitempty"`
}

// PassType — вид пропуска и требования к пакету заявки.
type PassType struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	RequiredFields []string          `json:"required_fields"`
	Patterns       map[string]string `json:"patterns,omitempty"`
	Documents      []Document        `json:"documents"`
	LeadDays       int               `json:"lead_days"`
	MinAge         int               `json:"min_age,omitempty"`
	Assumption     bool              `json:"assumption,omitempty"`

	compiled map[string]*regexp.Regexp
}

// Document — документ в составе пакета.
type Document struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Required bool   `json:"required"`
}

// Renewal — политика ежегодного продления пропусков.
type Renewal struct {
	ValidDays        int  `json:"valid_days"`
	SubmitBeforeDays int  `json:"submit_before_days"`
	WarnDays         int  `json:"warn_days"`
	Assumption       bool `json:"assumption,omitempty"`
}

// Pattern возвращает скомпилированное регулярное выражение для поля.
func (p *PassType) Pattern(field string) *regexp.Regexp { return p.compiled[field] }

// Scenario ищет сценарий по идентификатору.
func (p *Profile) Scenario(id string) (*Scenario, bool) {
	for i := range p.Scenarios {
		if p.Scenarios[i].ID == id {
			return &p.Scenarios[i], true
		}
	}
	return nil, false
}

// PassType ищет вид пропуска по идентификатору.
func (p *Profile) PassType(id string) (*PassType, bool) {
	for i := range p.PassTypes {
		if p.PassTypes[i].ID == id {
			return &p.PassTypes[i], true
		}
	}
	return nil, false
}

// Workdays строит производственный календарь профиля.
func (p *Profile) Workdays() engine.Workdays {
	hs := make([]time.Time, 0, len(p.Holidays))
	for _, h := range p.Holidays {
		if t, err := time.Parse("2006-01-02", h); err == nil {
			hs = append(hs, t)
		}
	}
	return engine.NewWorkdays(hs)
}

// Parse разбирает и проверяет профиль.
func Parse(data []byte) (*Profile, error) {
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("разбор профиля: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("профиль %q: %w", p.ID, err)
	}
	return &p, nil
}

func (p *Profile) validate() error {
	if p.ID == "" || p.Name == "" {
		return fmt.Errorf("не заданы id и name")
	}
	for _, h := range p.Holidays {
		if _, err := time.Parse("2006-01-02", h); err != nil {
			return fmt.Errorf("праздник %q: неверная дата", h)
		}
	}
	owners := map[string]bool{"applicant": true}
	for _, a := range p.Authorities {
		owners[a.ID] = true
	}
	for _, a := range p.Authorities {
		if a.ReportsTo != "" && !owners[a.ReportsTo] {
			return fmt.Errorf("оргструктура: %q подчинён неизвестному %q", a.ID, a.ReportsTo)
		}
	}
	for i := range p.PassTypes {
		pt := &p.PassTypes[i]
		pt.compiled = make(map[string]*regexp.Regexp, len(pt.Patterns))
		for field, expr := range pt.Patterns {
			re, err := regexp.Compile(expr)
			if err != nil {
				return fmt.Errorf("пропуск %q, поле %q: %w", pt.ID, field, err)
			}
			pt.compiled[field] = re
		}
	}
	if p.Renewal.SubmitBeforeDays <= 0 || p.Renewal.ValidDays <= 0 {
		return fmt.Errorf("не задана политика продления")
	}
	if len(p.Scenarios) == 0 {
		return fmt.Errorf("нет сценариев")
	}
	seen := map[string]bool{}
	for _, sc := range p.Scenarios {
		if seen[sc.ID] {
			return fmt.Errorf("дублируется сценарий %q", sc.ID)
		}
		seen[sc.ID] = true
		if _, ok := p.PassType(sc.PassType); !ok {
			return fmt.Errorf("сценарий %q: неизвестный вид пропуска %q", sc.ID, sc.PassType)
		}
		questions := map[string]bool{}
		for _, q := range sc.Questions {
			questions[q.ID] = true
		}
		ids := map[string]bool{}
		steps := make([]engine.Step, 0, len(sc.Steps))
		for _, st := range sc.Steps {
			ids[st.ID] = true
			if st.Calendar != engine.CalendarDays && st.Calendar != engine.WorkingDays {
				return fmt.Errorf("шаг %q: calendar должен быть calendar или working", st.ID)
			}
			if st.MinDays != nil && *st.MinDays > st.Days || st.MaxDays != nil && *st.MaxDays < st.Days {
				return fmt.Errorf("шаг %q: нарушено min_days ≤ days ≤ max_days", st.ID)
			}
			if st.Owner != "" && !owners[st.Owner] {
				return fmt.Errorf("шаг %q: неизвестный исполнитель %q", st.ID, st.Owner)
			}
			for q := range st.When {
				if !questions[q] {
					return fmt.Errorf("шаг %q: условие по неизвестному вопросу %q", st.ID, q)
				}
			}
			steps = append(steps, engine.Step{ID: st.ID, Days: st.Days, Calendar: st.Calendar, DependsOn: st.DependsOn})
		}
		if !ids[sc.Goal] || !ids[sc.FirstAction] {
			return fmt.Errorf("сценарий %q: goal и first_action должны ссылаться на шаги", sc.ID)
		}
		// Полный граф сценария должен считаться без ошибок.
		if _, err := engine.Compute(steps, time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC), p.Workdays()); err != nil {
			return fmt.Errorf("сценарий %q: %w", sc.ID, err)
		}
	}
	return nil
}
