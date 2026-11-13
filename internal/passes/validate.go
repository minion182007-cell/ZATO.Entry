// Package passes — модуль пропусков: предпроверка пакета заявки до
// подачи и календарь ежегодных продлений.
package passes

import (
	"fmt"
	"strings"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/profile"
)

const layout = "2006-01-02"

// Person — человек в пакете заявки.
type Person struct {
	FullName            string   `json:"full_name"`
	BirthDate           string   `json:"birth_date"`
	Citizenship         string   `json:"citizenship"`
	PassportSeries      string   `json:"passport_series"`
	PassportNumber      string   `json:"passport_number"`
	PassportIssued      string   `json:"passport_issued"`
	RegistrationAddress string   `json:"registration_address"`
	Employer            string   `json:"employer"`
	Position            string   `json:"position"`
	Purpose             string   `json:"purpose"`
	Documents           []string `json:"documents"`
}

func (p Person) fields() map[string]string {
	return map[string]string{
		"full_name": p.FullName, "birth_date": p.BirthDate, "citizenship": p.Citizenship,
		"passport_series": p.PassportSeries, "passport_number": p.PassportNumber,
		"passport_issued": p.PassportIssued, "registration_address": p.RegistrationAddress,
		"employer": p.Employer, "position": p.Position, "purpose": p.Purpose,
	}
}

var fieldTitles = map[string]string{
	"full_name": "ФИО", "birth_date": "дата рождения", "citizenship": "гражданство",
	"passport_series": "серия паспорта", "passport_number": "номер паспорта",
	"passport_issued": "дата выдачи паспорта", "registration_address": "адрес регистрации",
	"employer": "работодатель", "position": "должность", "purpose": "цель въезда",
}

// Package — пакет заявки на пропуска.
type Package struct {
	City       string   `json:"city"`
	PassType   string   `json:"pass_type"`
	EntryDate  string   `json:"entry_date"`  // желаемая дата въезда
	SubmitDate string   `json:"submit_date"` // пусто — сегодня
	Persons    []Person `json:"persons"`
}

// Severity — серьёзность замечания.
type Severity string

const (
	Error   Severity = "error"   // пакет вернут
	Warning Severity = "warning" // риск срыва срока
)

// Issue — замечание предпроверки.
type Issue struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Person   int      `json:"person"` // индекс в пакете, -1 — пакет целиком
	Field    string   `json:"field,omitempty"`
	Message  string   `json:"message"`
}

// Report — результат предпроверки.
type Report struct {
	OK             bool `json:"ok"`
	PersonsChecked int  `json:"persons_checked"`
	PersonsReady   int  `json:"persons_ready"`
	Errors         int  `json:"errors"`
	Warnings       int  `json:"warnings"`
	// RealisticEntryDate — ближайшая реалистичная дата въезда при подаче
	// в submit_date с учётом срока рассмотрения из профиля.
	RealisticEntryDate string  `json:"realistic_entry_date"`
	Issues             []Issue `json:"issues"`
}

// Validate проверяет пакет по требованиям вида пропуска.
func Validate(pt *profile.PassType, pkg Package, today time.Time) (*Report, error) {
	submit := today
	if pkg.SubmitDate != "" {
		t, err := time.Parse(layout, pkg.SubmitDate)
		if err != nil {
			return nil, fmt.Errorf("submit_date: ожидается дата в формате ГГГГ-ММ-ДД")
		}
		submit = t
	}
	rep := &Report{PersonsChecked: len(pkg.Persons), Issues: []Issue{}}
	add := func(sev Severity, code string, person int, field, msg string) {
		rep.Issues = append(rep.Issues, Issue{sev, code, person, field, msg})
		if sev == Error {
			rep.Errors++
		} else {
			rep.Warnings++
		}
	}

	realistic := submit.AddDate(0, 0, pt.LeadDays)
	rep.RealisticEntryDate = realistic.Format(layout)
	entry := realistic
	if pkg.EntryDate == "" {
		add(Warning, "no_entry_date", -1, "entry_date", "Не указана желаемая дата въезда — срок проверен не будет.")
	} else if t, err := time.Parse(layout, pkg.EntryDate); err != nil {
		add(Error, "bad_date", -1, "entry_date", "Дата въезда: ожидается формат ГГГГ-ММ-ДД.")
	} else {
		entry = t
		switch {
		case t.Before(submit):
			add(Error, "entry_in_past", -1, "entry_date", "Дата въезда раньше даты подачи.")
		case t.Before(realistic):
			add(Warning, "late_submission", -1, "entry_date", fmt.Sprintf(
				"До въезда %d дн., а рассмотрение занимает около %d. Реалистичная дата — %s.",
				int(t.Sub(submit).Hours()/24), pt.LeadDays, realistic.Format("02.01.2006")))
		}
	}
	if len(pkg.Persons) == 0 {
		add(Error, "empty_package", -1, "persons", "В пакете нет ни одного человека.")
	}

	seen := map[string]int{}
	for i, person := range pkg.Persons {
		before := rep.Errors
		fields := person.fields()
		for _, f := range pt.RequiredFields {
			if strings.TrimSpace(fields[f]) == "" {
				add(Error, "missing_field", i, f, "Не заполнено поле: "+fieldTitles[f]+".")
			}
		}
		for f, v := range fields {
			if re := pt.Pattern(f); re != nil && v != "" && !re.MatchString(v) {
				add(Error, "bad_format", i, f, "Неверный формат поля: "+fieldTitles[f]+".")
			}
		}

		birth, birthOK := parseDate(person.BirthDate)
		issued, issuedOK := parseDate(person.PassportIssued)
		if person.BirthDate != "" && !birthOK {
			add(Error, "bad_date", i, "birth_date", "Дата рождения: ожидается формат ГГГГ-ММ-ДД.")
		}
		if person.PassportIssued != "" && !issuedOK {
			add(Error, "bad_date", i, "passport_issued", "Дата выдачи паспорта: ожидается формат ГГГГ-ММ-ДД.")
		}
		if birthOK {
			switch age := ageOn(birth, entry); {
			case birth.After(submit):
				add(Error, "bad_date", i, "birth_date", "Дата рождения в будущем.")
			case pt.MinAge > 0 && age < pt.MinAge:
				add(Error, "under_age", i, "birth_date", fmt.Sprintf("На дату въезда человеку %d, требуется от %d лет.", age, pt.MinAge))
			}
		}
		if issuedOK && issued.After(submit) {
			add(Error, "bad_date", i, "passport_issued", "Дата выдачи паспорта в будущем.")
		}
		if birthOK && issuedOK && isRussian(person.Citizenship) {
			if due, ok := replacementDue(birth, issued, entry); ok {
				add(Error, "passport_needs_replacement", i, "passport_issued", fmt.Sprintf(
					"Паспорт выдан до %d-летия и на дату въезда уже недействителен — нужна замена.", due))
			}
		}

		have := map[string]bool{}
		for _, d := range person.Documents {
			have[d] = true
		}
		for _, d := range pt.Documents {
			if d.Required && !have[d.ID] {
				add(Error, "missing_document", i, d.ID, "Нет документа: "+d.Title+".")
			}
		}

		if key := person.PassportSeries + person.PassportNumber; key != "" {
			if j, dup := seen[key]; dup {
				add(Error, "duplicate_person", i, "passport_number", fmt.Sprintf("Тот же паспорт, что у человека №%d.", j+1))
			} else {
				seen[key] = i
			}
		}
		if rep.Errors == before {
			rep.PersonsReady++
		}
	}
	rep.OK = rep.Errors == 0
	return rep, nil
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse(layout, s)
	return t, err == nil
}

func ageOn(birth, on time.Time) int {
	age := on.Year() - birth.Year()
	if on.Month() < birth.Month() || on.Month() == birth.Month() && on.Day() < birth.Day() {
		age--
	}
	return age
}

func isRussian(c string) bool {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "рф", "россия", "российская федерация", "ru", "rus":
		return true
	}
	return false
}

// replacementDue проверяет замену паспорта гражданина РФ в 20 и 45 лет:
// паспорт, выданный до достижения возраста, действует ещё 90 дней после
// дня рождения.
func replacementDue(birth, issued, on time.Time) (int, bool) {
	for _, age := range []int{45, 20} {
		birthday := birth.AddDate(age, 0, 0)
		if issued.Before(birthday) && on.After(birthday.AddDate(0, 0, 90)) {
			return age, true
		}
	}
	return 0, false
}
