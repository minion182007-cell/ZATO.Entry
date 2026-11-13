// Package engine — территориально-независимое ядро ZATO.Entry:
// граф шагов с зависимостями, расчёт дат и критический путь.
package engine

import (
	"fmt"
	"time"
)

// Step — узел графа: работа с длительностью и зависимостями.
type Step struct {
	ID        string
	Days      int // 0 — веха
	Calendar  CalendarKind
	DependsOn []string
}

// Scheduled — шаг с рассчитанными датами.
type Scheduled struct {
	Step
	// Start — первый день шага.
	Start time.Time
	// End — последний день шага (включительно). Для вехи равен Start.
	End time.Time
	// SlackDays — резерв: на сколько календарных дней шаг может
	// сдвинуться, не сдвигая итоговую дату.
	SlackDays int
	// Critical — шаг лежит на критическом пути.
	Critical bool

	next time.Time // первый день, когда могут стартовать последователи
}

// Schedule — результат расчёта.
type Schedule struct {
	Steps []Scheduled // в топологическом порядке
	// Finish — дата, к которой выполнены все шаги.
	Finish time.Time
	// CriticalPath — идентификаторы шагов критического пути по порядку.
	CriticalPath []string
}

// Step возвращает рассчитанный шаг по идентификатору.
func (s Schedule) Step(id string) (Scheduled, bool) {
	for _, st := range s.Steps {
		if st.ID == id {
			return st, true
		}
	}
	return Scheduled{}, false
}

// Compute рассчитывает даты всех шагов от даты start.
func Compute(steps []Step, start time.Time, wd Workdays) (Schedule, error) {
	start = Day(start)
	order, err := topoSort(steps)
	if err != nil {
		return Schedule{}, err
	}

	idx := make(map[string]int, len(order))
	out := make([]Scheduled, len(order))
	driving := make([][]int, len(order))

	// Прямой проход: ранние даты.
	for i, st := range order {
		idx[st.ID] = i
		es := start
		for _, dep := range st.DependsOn {
			if n := out[idx[dep]].next; n.After(es) {
				es = n
			}
		}
		for _, dep := range st.DependsOn {
			if out[idx[dep]].next.Equal(es) {
				driving[i] = append(driving[i], idx[dep])
			}
		}
		working := st.Calendar == WorkingDays
		if working {
			es = wd.Align(es)
		}
		sc := Scheduled{Step: st, Start: es}
		switch {
		case st.Days == 0:
			sc.End, sc.next = es, es
		case working:
			sc.next = wd.AddWorking(es, st.Days)
			sc.End = lastWorkingBefore(wd, sc.next)
		default:
			sc.next = es.AddDate(0, 0, st.Days)
			sc.End = sc.next.AddDate(0, 0, -1)
		}
		out[i] = sc
	}

	var projectEnd time.Time
	for _, sc := range out {
		if sc.next.After(projectEnd) {
			projectEnd = sc.next
		}
	}

	// Обратный проход: резервы.
	latestNext := make([]time.Time, len(out))
	for i := range latestNext {
		latestNext[i] = projectEnd
	}
	for i := len(out) - 1; i >= 0; i-- {
		span := daysBetween(out[i].Start, out[i].next)
		latestStart := latestNext[i].AddDate(0, 0, -span)
		out[i].SlackDays = daysBetween(out[i].Start, latestStart)
		for _, dep := range out[i].DependsOn {
			j := idx[dep]
			if latestStart.Before(latestNext[j]) {
				latestNext[j] = latestStart
			}
		}
	}

	// Критический путь: от шагов, определяющих итоговую дату, назад по
	// определяющим предшественникам.
	var mark func(i int)
	mark = func(i int) {
		if out[i].Critical {
			return
		}
		out[i].Critical = true
		// Разрыв из-за выходных и праздников резервом не является.
		out[i].SlackDays = 0
		for _, j := range driving[i] {
			mark(j)
		}
	}
	for i := range out {
		if out[i].next.Equal(projectEnd) {
			mark(i)
		}
	}

	res := Schedule{Steps: out}
	for _, sc := range out {
		if sc.End.After(res.Finish) {
			res.Finish = sc.End
		}
		if sc.Critical {
			res.CriticalPath = append(res.CriticalPath, sc.ID)
		}
	}
	return res, nil
}

func lastWorkingBefore(wd Workdays, d time.Time) time.Time {
	d = d.AddDate(0, 0, -1)
	for !wd.IsWorking(d) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// topoSort — алгоритм Кана, устойчивый к порядку входа.
func topoSort(steps []Step) ([]Step, error) {
	pos := make(map[string]int, len(steps))
	for i, st := range steps {
		if st.ID == "" {
			return nil, fmt.Errorf("шаг №%d без идентификатора", i+1)
		}
		if _, dup := pos[st.ID]; dup {
			return nil, fmt.Errorf("дублируется шаг %q", st.ID)
		}
		if st.Days < 0 {
			return nil, fmt.Errorf("шаг %q: отрицательная длительность", st.ID)
		}
		pos[st.ID] = i
	}
	indeg := make([]int, len(steps))
	succ := make([][]int, len(steps))
	for i, st := range steps {
		for _, dep := range st.DependsOn {
			j, ok := pos[dep]
			if !ok {
				return nil, fmt.Errorf("шаг %q зависит от неизвестного шага %q", st.ID, dep)
			}
			indeg[i]++
			succ[j] = append(succ[j], i)
		}
	}
	done := make([]bool, len(steps))
	order := make([]Step, 0, len(steps))
	for len(order) < len(steps) {
		progressed := false
		for i := range steps {
			if done[i] || indeg[i] != 0 {
				continue
			}
			done[i] = true
			progressed = true
			order = append(order, steps[i])
			for _, k := range succ[i] {
				indeg[k]--
			}
		}
		if !progressed {
			for i := range steps {
				if !done[i] {
					return nil, fmt.Errorf("циклическая зависимость с участием шага %q", steps[i].ID)
				}
			}
		}
	}
	return order, nil
}
