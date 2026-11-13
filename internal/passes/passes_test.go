package passes_test

import (
	"strings"
	"testing"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/passes"
	"github.com/minion182007-cell/ZATO.Entry/internal/profile"
	"github.com/minion182007-cell/ZATO.Entry/profiles"
)

var today = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)

func sarov(t *testing.T) *profile.Profile {
	t.Helper()
	reg, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := reg.Get("sarov")
	return p
}

// Данные сгенерированы, реальным людям не принадлежат.
func good() passes.Person {
	return passes.Person{
		FullName: "Тестов Тест Тестович", BirthDate: "1990-05-10", Citizenship: "РФ",
		PassportSeries: "0000", PassportNumber: "000001", PassportIssued: "2015-06-01",
		RegistrationAddress: "г. Тестовый, ул. Примерная, д. 1", Employer: "ООО «Пример»",
		Position: "Монтажник", Purpose: "Работы по договору",
		Documents: []string{"passport_copy", "questionnaire", "petition", "employment_proof"},
	}
}

func codes(r *passes.Report) string {
	var out []string
	for _, i := range r.Issues {
		out = append(out, i.Code)
	}
	return strings.Join(out, ",")
}

func TestValidPackage(t *testing.T) {
	pt, _ := sarov(t).PassType("work")
	r, err := passes.Validate(pt, passes.Package{EntryDate: "2027-03-01", Persons: []passes.Person{good()}}, today)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.PersonsReady != 1 || len(r.Issues) != 0 {
		t.Errorf("ожидался чистый пакет, замечания: %s", codes(r))
	}
	if r.RealisticEntryDate != "2027-01-30" {
		t.Errorf("realistic = %s", r.RealisticEntryDate)
	}
}

func TestValidationFindsProblems(t *testing.T) {
	pt, _ := sarov(t).PassType("work")

	noDoc := good()
	noDoc.PassportNumber = "000002"
	noDoc.Documents = []string{"passport_copy"}

	badFormat := good()
	badFormat.PassportNumber = "12-34"
	badFormat.Position = ""

	// 45 лет исполнилось 01.02.2026, паспорт выдан в 2001 году.
	oldPassport := good()
	oldPassport.PassportNumber = "000003"
	oldPassport.BirthDate = "1981-02-01"
	oldPassport.PassportIssued = "2001-03-01"

	dup := good()

	r, err := passes.Validate(pt, passes.Package{
		EntryDate: "2026-12-01",
		Persons:   []passes.Person{good(), noDoc, badFormat, oldPassport, dup},
	}, today)
	if err != nil {
		t.Fatal(err)
	}
	got := codes(r)
	for _, want := range []string{"late_submission", "missing_document", "bad_format", "missing_field", "passport_needs_replacement", "duplicate_person"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет замечания %s в %s", want, got)
		}
	}
	if r.OK || r.PersonsReady != 1 {
		t.Errorf("ok=%v ready=%d", r.OK, r.PersonsReady)
	}
}

func TestRenewalCalendar(t *testing.T) {
	pol := sarov(t).Renewal
	cal, err := passes.BuildCalendar(pol, passes.RenewalRequest{Staff: []passes.Employee{
		{ID: "E-1", PassExpires: "2027-06-01"}, // подача 17.04.2027 — planned
		{ID: "E-2", PassExpires: "2027-01-05"}, // подача 21.11.2026 — due
		{ID: "E-3", PassExpires: "2026-12-10"}, // подача 26.10.2026 — overdue
		{ID: "E-4", PassExpires: "2026-11-01"}, // expired
		{ID: "E-5", PassExpires: "2027-01-07"}, // подача 23.11.2026 — следующая неделя
		{ID: "E-6", PassExpires: "2027-01-04"}, // подача 20.11.2026 — одна неделя с E-2
	}}, today)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"E-1": "planned", "E-2": "due", "E-3": "overdue", "E-4": "expired", "E-5": "due", "E-6": "due"}
	for _, it := range cal.Items {
		if it.Status != want[it.ID] {
			t.Errorf("%s: %s, ожидалось %s", it.ID, it.Status, want[it.ID])
		}
		if it.ID == "E-2" && it.SubmitBy != "2026-11-21" {
			t.Errorf("E-2 submit_by = %s", it.SubmitBy)
		}
	}
	if cal.Items[0].ID != "E-4" {
		t.Errorf("первым должен идти самый срочный, а не %s", cal.Items[0].ID)
	}
	// Пакеты: E-3; E-6+E-2 (к 20.11); E-5; E-1.
	if len(cal.Batches) != 4 || len(cal.Batches[1].Staff) != 2 || cal.Batches[1].SubmitBy != "2026-11-20" {
		t.Errorf("пакеты: %+v", cal.Batches)
	}
	ics := cal.ICS()
	if strings.Count(ics, "BEGIN:VEVENT") != 4 || !strings.Contains(ics, "DTSTART;VALUE=DATE:20261120") {
		t.Errorf("ics:\n%s", ics)
	}
}
