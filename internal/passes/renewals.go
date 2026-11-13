package passes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/profile"
)

// Employee — сотрудник с действующим пропуском.
type Employee struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PassExpires string `json:"pass_expires"`
}

// RenewalRequest — запрос календаря продлений.
type RenewalRequest struct {
	City  string     `json:"city"`
	Today string     `json:"today"` // пусто — сегодня
	Staff []Employee `json:"staff"`
}

// Статусы продления.
const (
	StatusPlanned = "planned" // до подачи есть время
	StatusDue     = "due"     // пора подавать
	StatusOverdue = "overdue" // срок подачи пропущен, пропуск ещё действует
	StatusExpired = "expired" // пропуск истёк
)

// RenewalItem — строка календаря.
type RenewalItem struct {
	Employee
	SubmitBy     string `json:"submit_by"`
	DaysToSubmit int    `json:"days_to_submit"`
	DaysToExpiry int    `json:"days_to_expiry"`
	Status       string `json:"status"`
	NextExpires  string `json:"next_expires"`
}

// RenewalBatch — пакет продлений, который подаётся одной заявкой.
type RenewalBatch struct {
	SubmitBy string   `json:"submit_by"`
	Status   string   `json:"status"`
	Staff    []string `json:"staff"`
}

// RenewalCalendar — календарь продлений штата.
type RenewalCalendar struct {
	Today            string         `json:"today"`
	SubmitBeforeDays int            `json:"submit_before_days"`
	Counts           map[string]int `json:"counts"`
	Items            []RenewalItem  `json:"items"`
	Batches          []RenewalBatch `json:"batches"`
}

// BuildCalendar считает сроки подачи на продление: пакет подаётся за
// submit_before_days до истечения пропуска.
func BuildCalendar(pol profile.Renewal, req RenewalRequest, now time.Time) (*RenewalCalendar, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if req.Today != "" {
		t, err := time.Parse(layout, req.Today)
		if err != nil {
			return nil, fmt.Errorf("today: ожидается дата в формате ГГГГ-ММ-ДД")
		}
		today = t
	}
	cal := &RenewalCalendar{
		Today: today.Format(layout), SubmitBeforeDays: pol.SubmitBeforeDays,
		Counts: map[string]int{StatusPlanned: 0, StatusDue: 0, StatusOverdue: 0, StatusExpired: 0},
		Items:  []RenewalItem{}, Batches: []RenewalBatch{},
	}
	for i, e := range req.Staff {
		exp, err := time.Parse(layout, e.PassExpires)
		if err != nil {
			return nil, fmt.Errorf("сотрудник №%d: pass_expires — ожидается дата в формате ГГГГ-ММ-ДД", i+1)
		}
		submit := exp.AddDate(0, 0, -pol.SubmitBeforeDays)
		it := RenewalItem{
			Employee:     e,
			SubmitBy:     submit.Format(layout),
			DaysToSubmit: days(today, submit),
			DaysToExpiry: days(today, exp),
			NextExpires:  exp.AddDate(0, 0, pol.ValidDays).Format(layout),
		}
		switch {
		case exp.Before(today):
			it.Status = StatusExpired
		case submit.Before(today):
			it.Status = StatusOverdue
		case it.DaysToSubmit <= pol.WarnDays:
			it.Status = StatusDue
		default:
			it.Status = StatusPlanned
		}
		cal.Counts[it.Status]++
		cal.Items = append(cal.Items, it)
	}
	sort.SliceStable(cal.Items, func(i, j int) bool { return cal.Items[i].SubmitBy < cal.Items[j].SubmitBy })

	// Сотрудники со сроком подачи на одной неделе уходят одним пакетом;
	// пакет подаётся к самому раннему сроку.
	byWeek := map[string]int{}
	for _, it := range cal.Items {
		if it.Status == StatusExpired {
			continue
		}
		key := weekKey(it.SubmitBy)
		i, ok := byWeek[key]
		if !ok {
			i = len(cal.Batches)
			byWeek[key] = i
			cal.Batches = append(cal.Batches, RenewalBatch{SubmitBy: it.SubmitBy, Status: it.Status})
		}
		b := &cal.Batches[i]
		b.Staff = append(b.Staff, label(it.Employee))
		if rank(it.Status) > rank(b.Status) {
			b.Status = it.Status
		}
	}
	return cal, nil
}

func weekKey(date string) string {
	t, _ := time.Parse(layout, date)
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-%02d", y, w)
}

func rank(status string) int {
	switch status {
	case StatusOverdue:
		return 2
	case StatusDue:
		return 1
	}
	return 0
}

func label(e Employee) string {
	if e.Name != "" {
		return e.ID + " (" + e.Name + ")"
	}
	return e.ID
}

func days(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

// ICS выгружает сроки подачи в формате iCalendar.
func (c *RenewalCalendar) ICS() string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Startoken//ZATO.Entry//RU\r\nCALSCALE:GREGORIAN\r\n")
	stamp := strings.ReplaceAll(c.Today, "-", "") + "T000000Z"
	for i, batch := range c.Batches {
		t, _ := time.Parse(layout, batch.SubmitBy)
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:renewal-%s-%d@zato-entry\r\nDTSTAMP:%s\r\n", batch.SubmitBy, i, stamp)
		fmt.Fprintf(&b, "DTSTART;VALUE=DATE:%s\r\nDTEND;VALUE=DATE:%s\r\n", t.Format("20060102"), t.AddDate(0, 0, 1).Format("20060102"))
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", icsEscape(fmt.Sprintf("Подать пакет на продление пропусков (%d чел.)", len(batch.Staff))))
		fmt.Fprintf(&b, "DESCRIPTION:%s\r\nEND:VEVENT\r\n", icsEscape(strings.Join(batch.Staff, ", ")))
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func icsEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r", "", "\n", `\n`).Replace(s)
}
