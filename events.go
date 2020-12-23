package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event .
//
//
// The Action field is one of a fixed set of action descriptions:
//    run    - the test has started running
//    pause  - the test has been paused
//    cont   - the test has continued running
//    pass   - the test passed
//    bench  - the benchmark printed log output but did not fail
//    fail   - the test or benchmark failed
//    output - the test printed output
//    skip   - the test was skipped or the package contained no tests
//
type Event struct {
	Time    time.Time // encodes as an RFC3339-format string
	Action  Action
	Package string
	Test    string
	Elapsed float64 // seconds
	Output  string
}

func (t Event) Key() Key {
	return Key{
		Package: t.Package,
		Test:    t.Test,
	}
}

// Key identifies a package and test together.
type Key struct {
	Package string
	Test    string
}

func (t Key) String() string {
	if t.Test == "" {
		return t.Package
	}
	return t.Package + "." + t.Test
}

type Events []Event

func (es Events) Clone() Events {
	events := make(Events, len(es))
	for k, v := range es {
		events[k] = v
	}
	return events
}

func (es Events) Status() Status {
	for _, e := range es {
		switch e.Action {

		case ActionFail:
			return StatusFail

		case ActionPass:
			return StatusPass

		case ActionSkip:
			return StatusSkip

		case ActionBench:
			return StatusBench
		}
	}
	return StatusNone
}

func (es Events) FindFirstByAction(actions ...Action) *Event {
	for _, v := range es {
		for _, action := range actions {
			if v.Action == action {
				return &v
			}
		}
	}
	return nil
}

func (es Events) SortByTime() {
	sort.SliceStable(es, func(i, j int) bool {
		return es[i].Time.Before(es[j].Time)
	})

}

// Compact removes events that are uninteresting for printing
func (es Events) Compact() Events {
	var (
		failedAt  float64
		passedAt  float64
		skippedAt float64
	)

	if e := es.FindFirstByAction(ActionPass); e != nil {
		passedAt = e.Elapsed
	}

	if e := es.FindFirstByAction(ActionFail); e != nil {
		failedAt = e.Elapsed
	}

	if e := es.FindFirstByAction(ActionSkip); e != nil {
		skippedAt = e.Elapsed
	}

	var v Events

loop:
	for _, e := range es {
		output := strings.TrimLeft(e.Output, " ")
		outputWS := strings.TrimSpace(e.Output)
		if e.Action == "run" ||
			e.Action == "cont" ||
			e.Action == "pause" ||
			(e.Action == "output" && e.Test != "" &&
				((output == fmt.Sprintf("=== RUN   %s\n", e.Test)) ||
					(output == fmt.Sprintf("=== CONT  %s\n", e.Test)) ||
					(output == fmt.Sprintf("=== PAUSE %s\n", e.Test)) ||
					(output == fmt.Sprintf("--- FAIL: %s (%.2fs)\n", e.Test, failedAt)) ||
					(output == fmt.Sprintf("--- SKIP: %s (%.2fs)\n", e.Test, skippedAt)) ||
					(output == fmt.Sprintf("--- PASS: %s (%.2fs)\n", e.Test, passedAt)))) ||
			(e.Action == "output" && e.Package != "" && e.Test == "" &&
				((strings.HasPrefix(output, fmt.Sprintf("ok  	%s", e.Package))) ||
					(strings.HasSuffix(output, "[no test files]\n")) ||
					(output == fmt.Sprintf("ok   %s\n", e.Package)) ||
					(output == "PASS\n") ||
					(output == "FAIL\n") ||
					(strings.HasPrefix(outputWS, fmt.Sprintf("FAIL\t%s\t", e.Package))) ||
					(strings.HasPrefix(outputWS, "coverage:") && strings.HasSuffix(outputWS, "of statements")))) {
			continue loop
		}
		v = append(v, e)
	}
	return v
}

func (es Events) IsPackageWithoutTest() bool {
	for _, e := range es {
		output := strings.TrimLeft(e.Output, " ")
		if e.Action == "output" &&
			e.Package != "" &&
			e.Test == "" &&
			(strings.HasSuffix(output, "[no test files]\n")) {
			return true
		}
	}
	return false
}

func (es Events) FindCoverage() string {
	if len(es) == 0 {
		return ""
	}
	if es[0].Package == "" || es[0].Test != "" {
		return ""
	}
	for _, event := range es {
		if event.Action != ActionOutput {
			continue
		}
		output := strings.TrimSpace(event.Output)
		if strings.HasPrefix(output, "coverage: ") && strings.HasSuffix(output, " of statements") {
			output = strings.TrimPrefix(output, "coverage:")
			output = strings.TrimSuffix(output, "of statements")
			output = strings.TrimSpace(output)
			return output
		}
	}
	return ""
}

func (es Events) PrintDetail(V Verbosity) {
	if len(es) == 0 {
		return
	}
	events := es.Clone()
	if V <= V3 {
		events = events.Compact()
	}
	if len(events) == 0 {
		return
	}
	events.SortByTime()
	status := events.Status()
	var event *Event
	switch status {
	case StatusFail:
		event = events.FindFirstByAction(ActionFail)
	case StatusPass:
		event = events.FindFirstByAction(ActionPass)
	case StatusSkip:
		event = events.FindFirstByAction(ActionSkip)
	case StatusBench:
		event = events.FindFirstByAction(ActionBench)
	}
	if event == nil {
		event = &events[0]
	}

	var testName string
	if event.Test != "" {
		testName = "." + testColor(event.Test)
	}

	var sb strings.Builder
	if event.Elapsed >= 0.01 {
		sb.WriteString("  ")
		sb.WriteString(timeColor(fmt.Sprintf("(%.2fs)", event.Elapsed)))
	}

	coverage := es.FindCoverage()
	if len(coverage) > 0 {
		sb.WriteString("  ")
		sb.WriteString(coverColor(fmt.Sprintf("{%s}", coverage)))
	}

	if es.IsPackageWithoutTest() {
		sb.WriteString("  ")
		sb.WriteString("[no tests]")
	}

	var filteredEvents Events
loop:
	for _, e := range events {
		if V <= V3 && strings.TrimSpace(e.Output) == "" {
			continue loop
		}
		filteredEvents = append(filteredEvents, e)
	}

	statusColor := statusColors[status]
	fmt.Print(statusColor("═══") +
		" " + statusColor(statusNames[status]) +
		" " + statusColor(event.Package) + testName +
		sb.String() +
		"\n",
	)
	if len(filteredEvents) > 0 {
		fmt.Println("")
	}
	for _, e := range filteredEvents {
		var ss []string
		if V >= V3 {
			ss = append(ss, fmt.Sprintf("%7s", e.Action))
		}
		if V >= V2 {
			ss = append(ss, e.Time.Format("15:04:05.999"))
		}
		ss = append(ss, strings.TrimSuffix(e.Output, "\n"), "\n")
		fmt.Print(strings.Join(ss, " "))
	}
	if len(filteredEvents) > 0 {
		fmt.Println("")
	}
}
