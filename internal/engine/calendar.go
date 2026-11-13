package engine

import "time"

// CalendarKind определяет, в каких днях измеряется длительность шага.
type CalendarKind string

const (
	// CalendarDays — календарные дни (например, срок спецпроверки).
	CalendarDays CalendarKind = "calendar"
	// WorkingDays — рабочие дни (например, подготовка документов).
	WorkingDays CalendarKind = "working"
)

// Workdays — производственный календарь: суббота, воскресенье и
// перечисленные праздники считаются нерабочими.
type Workdays struct {
	holidays map[string]bool
}

const dateLayout = "2006-01-02"

// NewWorkdays создаёт календарь с праздничными днями.
func NewWorkdays(holidays []time.Time) Workdays {
	m := make(map[string]bool, len(holidays))
	for _, h := range holidays {
		m[h.Format(dateLayout)] = true
	}
	return Workdays{holidays: m}
}

// IsWorking сообщает, рабочий ли день.
func (w Workdays) IsWorking(d time.Time) bool {
	switch d.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	return !w.holidays[d.Format(dateLayout)]
}

// Align возвращает ближайший рабочий день, начиная с d включительно.
func (w Workdays) Align(d time.Time) time.Time {
	for !w.IsWorking(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// AddWorking возвращает день, следующий за n-м рабочим днём, считая с d.
// d должен быть рабочим днём (см. Align).
func (w Workdays) AddWorking(d time.Time, n int) time.Time {
	for n > 0 {
		if w.IsWorking(d) {
			n--
		}
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// Day обрезает время до даты в UTC.
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func daysBetween(a, b time.Time) int {
	return int(b.Sub(a).Hours() / 24)
}
