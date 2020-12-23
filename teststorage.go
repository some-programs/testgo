package main

import (
	"fmt"
	"sort"

	"github.com/maruel/natural"
)

type TestStorage map[Key]Events

func (ts TestStorage) OrderedKeys() []Key {
	var tks []Key
	for k := range ts {
		tks = append(tks, k)
	}
	sort.SliceStable(tks, func(i, j int) bool {
		return natural.Less(tks[i].String(), tks[j].String())
	})
	return tks
}

// Append event into tests
func (ts TestStorage) Append(e Event) {
	key := e.Key()
	events, _ := ts[key]
	events = append(events, e)
	ts[key] = events
}

func (ts TestStorage) Union(values ...TestStorage) TestStorage {
	tests := make(TestStorage, 0)
	for _, values := range values {
		for k, v := range values {
			tests[k] = v
		}
	}
	return tests
}

func (ts TestStorage) FilterPackageResults() TestStorage {
	tests := make(TestStorage, 0)
	for key, events := range ts {
		if key.Test != "" {
			tests[key] = events
		}
	}
	return tests
}

func (ts TestStorage) FilterKeys(exclude map[Key]bool) TestStorage {
	tests := make(TestStorage, 0)
loop:
	for key, events := range ts {
		if !exclude[key] {
			tests[key] = events
			continue loop
		}
	}
	return tests
}

func (ts TestStorage) FindByAction(action Action) TestStorage {
	tests := make(TestStorage, 0)
loop:
	for key, events := range ts {
		for _, e := range events {
			if e.Action == action {
				tests[key] = events
				continue loop
			}
		}
	}
	return tests
}

func (ts TestStorage) FilterAction(actions ...Action) TestStorage {
	actionMatch := make(map[Action]bool, len(actions))
	for _, action := range actions {
		actionMatch[action] = true
	}
	tests := make(TestStorage, 0)
loop:
	for key, events := range ts {
		for _, e := range events {
			if actionMatch[e.Action] {
				continue loop
			}
		}
		tests[key] = events
	}
	return tests
}

func (ts TestStorage) WithCoverage() TestStorage {
	tests := make(TestStorage, 0)
loop:
	for key, events := range ts {
		if key.Test != "" || key.Package == "" {
			continue loop
		}
		cov := events.FindCoverage()
		if cov != "" {
			tests[key] = events
		}
	}
	return tests
}

func (ts TestStorage) FilterNotests() TestStorage {
	tests := make(TestStorage, 0)
loop:
	for key, events := range ts {
		if events.IsPackageWithoutTest() {
			continue loop
		}
		tests[key] = events
	}
	return tests
}

func (ts TestStorage) CountTests() int {
	return len(ts.FilterPackageResults())
}

func (ts TestStorage) PrintSummary(status Status) {
	// count := ts.CountTests()
	statusColor := statusColors[status]
	header := statusColor(statusNames[status])
	hr := statusColor("════════════")
	prefix := statusColor(fmt.Sprintf("%6s ", statusNames[status]))

	fmt.Println(hr, header, hr)
	for _, key := range ts.OrderedKeys() {
		events := ts[key]
		var elapsed string
		if fe := events.FindFirstByAction(EndingActions...); fe != nil {
			if fe.Elapsed >= 0.01 {
				elapsed = timeColor(fmt.Sprintf("(%.2fs)", fe.Elapsed))
			}
		}
		if key.Test == "" {
			coverage := events.FindCoverage()
			if len(coverage) > 0 {
				coverage = fmt.Sprintf("  {%s} ", coverage)
			}
			fmt.Print(prefix +
				packageColor(key.Package) +
				" " + timeColor(elapsed) +
				coverColor(coverage) +
				"\n",
			)
		} else {
			fmt.Print(prefix +
				packageColor(key.Package) +
				"." + testColor(key.Test) +
				" " + timeColor(elapsed) +
				"\n",
			)
		}
	}
}

func (ts TestStorage) PrintCoverage() {
	hr := coverColor("════════════")
	var prefix string

	fmt.Println(hr, coverColor("COVR"), hr)
	for _, key := range ts.OrderedKeys() {
		events := ts[key]
		if key.Test == "" {
			coverage := events.FindCoverage()
			if len(coverage) > 0 {
				coverage = fmt.Sprintf("%6s ", coverage)
			}
			fmt.Print(prefix +
				coverColor(coverage) +
				packageColor(key.Package) +
				"\n",
			)
		}
	}
}
