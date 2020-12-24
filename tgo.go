package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/maruel/natural"
	"github.com/peterbourgon/ff/v3"
)

type Verbosity int

var (
	V0 = Verbosity(0) // default
	V1 = Verbosity(1) // minor changes
	V2 = Verbosity(2) // a few more details
	V3 = Verbosity(3) // more stuff
	V4 = Verbosity(4) // debug
	V5 = Verbosity(5) // (reserverd)
)

var (
	ActionRun    = Action("run")
	ActionPause  = Action("pause")
	ActionCont   = Action("cont")
	ActionPass   = Action("pass")
	ActionBench  = Action("bench")
	ActionFail   = Action("fail")
	ActionOutput = Action("output")
	ActionSkip   = Action("skip")

	AllActions = Actions{
		ActionRun, ActionPause, ActionCont, ActionPass,
		ActionBench, ActionFail, ActionOutput, ActionSkip,
	}

	EndingActions = Actions{ActionFail, ActionSkip, ActionPass, ActionBench}
)

var (
	StatusPass  = Status(ActionPass)
	StatusFail  = Status(ActionFail)
	StatusSkip  = Status(ActionSkip)
	StatusBench = Status(ActionBench)
	StatusNone  = Status("none")

	AllStatuses = Statuses{
		StatusBench,
		StatusPass,
		StatusSkip,
		StatusNone,
		StatusFail,
	}
	DefaultStatuses = Statuses{
		StatusNone,
		StatusFail,
	}

	statusNames = map[Status]string{
		StatusFail:  "FAIL",
		StatusPass:  "PASS",
		StatusNone:  "NONE",
		StatusSkip:  "SKIP",
		StatusBench: "BENCH",
	}
)

var (
	defaultColor  = color.New().SprintFunc()
	lineColor     = color.New().SprintFunc()
	hardLineColor = color.New().SprintFunc()
	packageColor  = color.New().SprintFunc()
	testColor     = color.New(color.FgMagenta).SprintFunc()
	testColorBold = color.New(color.FgMagenta, color.Bold).SprintFunc()
	timeColor     = color.New(color.FgCyan).SprintFunc()
	coverColor    = color.New(color.FgBlue).SprintFunc()

	failColor     = color.New(color.FgRed).SprintFunc()
	failColorBold = color.New(color.FgRed, color.Bold).SprintFunc()

	noneColor     = color.New(color.FgYellow).SprintFunc()
	noneColorBold = color.New(color.FgYellow, color.Bold).SprintFunc()

	passColor     = color.New(color.FgGreen).SprintFunc()
	passColorBold = color.New(color.FgGreen, color.Bold).SprintFunc()

	skipColor     = color.New(color.FgHiMagenta).SprintFunc()
	skipColorBold = color.New(color.FgHiMagenta, color.Bold).SprintFunc()

	statusColors = map[Status](func(a ...interface{}) string){
		StatusFail:  failColor,
		StatusPass:  passColor,
		StatusNone:  noneColor,
		StatusSkip:  skipColor,
		StatusBench: passColor,
	}

	statusColorsBold = map[Status](func(a ...interface{}) string){
		StatusFail:  failColorBold,
		StatusPass:  passColorBold,
		StatusNone:  noneColorBold,
		StatusSkip:  skipColorBold,
		StatusBench: passColorBold,
	}
)

// Flags .
type Flags struct {
	V                Verbosity
	Config           string
	Results          Statuses
	HideEmptyResults Statuses
	Summary          Statuses
	Bin              string
	All              bool
}

func (f *Flags) Register(fs *flag.FlagSet) {
	f.Results = Statuses{StatusFail, StatusNone}
	f.Summary = Statuses{StatusFail, StatusNone}

	fs.StringVar(&f.Bin, "bin", "go", "go binary name")
	fs.Var(&f.Results, "results", "types of results to show")
	fs.Var(&f.Summary, "summary", "types of summary to show")
	fs.Var(&f.HideEmptyResults, "res-hide", "hide emtpy results")
	fs.IntVar((*int)(&f.V), "v", 0, "0(lowest) to 5(highest)")
	fs.StringVar(&f.Config, "config", "", "config file")
	fs.BoolVar(&f.All, "all", false, "show mostly everything")

}

func (f *Flags) Setup(args []string) {
	if f.All {
		f.Results = AllStatuses
		f.Summary = AllStatuses
		f.HideEmptyResults = Statuses{}
	}
	for _, v := range args {
		if v == "-v" {
			f.V = V2
			f.Results = Statuses{
				// StatusSkip,
				StatusBench,
				StatusPass,
				StatusNone,
				StatusFail,
			}
			f.HideEmptyResults = Statuses{
				// StatusSkip,
				StatusBench,
				StatusPass,
				StatusNone,
				// StatusFail,
			}
			f.Summary = Statuses{
				StatusNone,
				StatusFail,
				// StatusPass,
			}
		}
	}

}

type Action string

func (a Action) String() string {
	return string(a)
}

func (a Action) IsStatus(s Status) bool {
	switch s {
	case StatusBench:
		return (a == ActionBench)

	case StatusPass:
		return (a == ActionPass)

	case StatusFail:
		return (a == ActionFail)

	case StatusSkip:
		return (a == ActionSkip)

	default:
		return false
	}
}

type Actions []Action

// Status is mostly like Actions but only for end states including 'none' which
// means that tests never reported as finished.
type Status string

func (s Status) IsAction(a Action) bool {
	switch a {
	case ActionBench:
		return s == StatusBench

	case ActionPass:
		return s == StatusPass

	case ActionFail:
		return s == StatusFail

	case ActionSkip:
		return s == StatusSkip

	default:
		return false
	}
}

func (s Status) String() string {
	return string(s)
}

type Statuses []Status

func (ss Statuses) Any(statuses ...Status) bool {
	for _, s := range ss {
		for _, status := range statuses {
			if s == status {
				return true
			}
		}
	}
	return false
}

func (ss Statuses) HasAction(action Action) bool {
	for _, s := range ss {
		if s.IsAction(action) {
			return true
		}
	}
	return false
}

// for flag
func (ss *Statuses) String() string {
	var r []string
	for _, v := range *ss {
		r = append(r, string(v))
	}
	return strings.Join(r, ",")
}

// for flag
func (ss *Statuses) Set(value string) error {
	value = strings.ToLower(value)
	switch value {
	case "-":
		*ss = make([]Status, 0)
		return nil
	case "all":
		*ss = make([]Status, len(AllStatuses))
		copy(*ss, AllStatuses)
		return nil
	}
	split := strings.Split(value, ",")
	var statuses Statuses
	for _, v := range split {
		if !AllStatuses.Any(Status(v)) {
			return fmt.Errorf("%s is not a valid status", v)
		}
		statuses = append(statuses, Status(v))
	}

	*ss = statuses
	return nil
}

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
					(output == "testing: warning: no tests to run\n") ||
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

func (es Events) PrintDetail(flags Flags) {
	if len(es) == 0 {
		return
	}
	events := es.Clone()
	if flags.V <= V3 {
		events = events.Compact()
	}
	if len(events) == 0 {
		return
	}

	var filteredEvents Events
loop:
	for _, e := range events {
		if flags.V <= V3 && strings.TrimSpace(e.Output) == "" {
			continue loop
		}
		filteredEvents = append(filteredEvents, e)
	}

	events.SortByTime()
	status := events.Status()
	numberEvents := len(filteredEvents)
	if numberEvents == 0 && flags.HideEmptyResults.Any(status) {
		return
	}
	textColor := defaultColor
	var event *Event
	switch status {
	case StatusFail:
		event = events.FindFirstByAction(ActionFail)
		textColor = failColor
	case StatusPass:
		event = events.FindFirstByAction(ActionPass)
		// textColor = passColor
	case StatusSkip:
		event = events.FindFirstByAction(ActionSkip)
		textColor = skipColor
	case StatusBench:
		event = events.FindFirstByAction(ActionBench)
	}

	if event == nil {
		event = &events[0]
	}

	var testName string
	if event.Test != "" {
		c := testColor
		if numberEvents > 0 {
			c = testColorBold
		}
		testName = "." + c(event.Test)
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

	statusColor := statusColors[status]
	statusBold := statusColorsBold[status]
	fmt.Print(statusBold("===") +
		" " + statusBold(statusNames[status]) +
		" " + statusColor(event.Package) + testName +
		sb.String() +
		"\n",
	)
	if len(filteredEvents) > 0 {
		fmt.Println("")
	}
	for _, e := range filteredEvents {
		var ss []string
		if flags.V >= V3 {
			ss = append(ss, fmt.Sprintf("%7s", e.Action))
		}
		if flags.V >= V3 {
			ss = append(ss, e.Time.Format("15:04:05.999"))
		}
		ss = append(ss, textColor(strings.TrimSuffix(e.Output, "\n")), "\n")
		fmt.Print(strings.Join(ss, " "))
	}
	if len(filteredEvents) > 0 {
		fmt.Println("")
	}
}

type TestStorage map[Key]Events

func (ts TestStorage) OrderedKeys() []Key {
	var tks []Key
	for k := range ts {
		tks = append(tks, k)
	}
	sort.SliceStable(tks, func(i, j int) bool {
		if (tks[i].Package == tks[j].Package) &&
			(tks[i].Test == "" || tks[j].Test == "") {
			return len(tks[i].Test) > len(tks[j].Test)
		}
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

func (ts TestStorage) FindPackageResults() TestStorage {
	tests := make(TestStorage, 0)
	for key, events := range ts {
		if key.Test == "" {
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

func (ts TestStorage) FindPackageTests(name string) TestStorage {
	tests := make(TestStorage, 0)
loop:
	for key, events := range ts {
		if name == key.Package {
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

func (ts TestStorage) PrintShortSummary(status Status) {
	statusColor := statusColors[status]
	statusBold := statusColorsBold[status]
	header := statusBold(statusNames[status])
	hr := statusColor("════════════")
	prefix := statusColor(fmt.Sprintf("%6s ", statusNames[status]))

	tests := ts.FindPackageResults()

	fmt.Println(hr, header, hr)
	for _, key := range tests.OrderedKeys() {
		events := ts[key]

		var sb strings.Builder

		if fe := events.FindFirstByAction(EndingActions...); fe != nil && fe.Elapsed >= 0.01 {
			sb.WriteString("  ")
			sb.WriteString(timeColor(fmt.Sprintf("(%.2fs)", fe.Elapsed)))
		}

		count := ts.FindPackageTests(key.Package).CountTests()
		sb.WriteString("   ")
		sb.WriteString(statusColor(fmt.Sprintf("<%v tests>", count)))

		if events.IsPackageWithoutTest() {
			sb.WriteString("  ")
			sb.WriteString("[no tests]")
		}

		coverage := events.FindCoverage()
		if len(coverage) > 0 {
			sb.WriteString("  ")
			sb.WriteString(coverColor(fmt.Sprintf("{%s}", coverage)))
		}
		fmt.Print(prefix +
			packageColor(key.Package) +
			sb.String() +
			"\n",
		)

	}

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

		var sb strings.Builder

		if fe := events.FindFirstByAction(EndingActions...); fe != nil && fe.Elapsed >= 0.01 {
			sb.WriteString("  ")
			sb.WriteString(timeColor(fmt.Sprintf("(%.2fs)", fe.Elapsed)))
		}
		if key.Test == "" {
			if events.IsPackageWithoutTest() {
				sb.WriteString("  ")
				sb.WriteString("[no tests]")
			}
			coverage := events.FindCoverage()
			if len(coverage) > 0 {
				sb.WriteString("  ")
				sb.WriteString(coverColor(fmt.Sprintf("{%s}", coverage)))
			}
			fmt.Print(prefix +
				packageColor(key.Package) +
				sb.String() +
				"\n",
			)
		} else {
			fmt.Print(prefix +
				packageColor(key.Package) +
				"." + testColor(key.Test) +
				sb.String() +
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

type ExitError int

func (e ExitError) Error() string {
	return strconv.FormatInt(int64(e), 10)
}

func main() {
	log.SetFlags(log.Lshortfile)
	fs := flag.NewFlagSet("tgo", flag.ExitOnError)

	var flags Flags
	flags.Register(fs)

	if err := ff.Parse(fs, nil,
		ff.WithEnvVarPrefix("TGO"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
	); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	flags.Setup(os.Args)

	if flags.V <= V3 {
		log.SetOutput(ioutil.Discard)
	}

	log.Printf("flags %+v", flags)
	log.Printf("args: %+v", os.Args)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill, syscall.SIGTERM)
	defer func() {
		signal.Stop(c)
		cancel()
	}()
	go func() {
		select {
		case <-c:
			cancel()
			return
		case <-ctx.Done():
			return
		}
	}()

	if err := run(ctx, flags, os.Args[1:]); err != nil {
		var ee ExitError
		if errors.As(err, &ee) {
			os.Exit(int(ee))
		}
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, flags Flags, argv []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		coverEnabled bool
	)
	for _, v := range argv {
		if v == "-cover" {
			coverEnabled = true
		}
	}

	args := []string{"test", "-json"}
	args = append(args, argv...)
	log.Println("args", args)
	cmd := exec.CommandContext(ctx, flags.Bin, args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		fmt.Println(err)
		return err
	}

	t0 := time.Now()

	tests := make(TestStorage, 0)
	printed := make(map[Key]bool, 0)
	scanner := bufio.NewScanner(stdout)

	fmt.Println("*****")
scan:
	for scanner.Scan() {

		var e Event
		log.Println("LINE:", scanner.Text())
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			log.Println("scanner error", err)
			continue scan
		}
		tests.Append(e)
		key := e.Key()
		if !printed[key] && flags.Results.HasAction(e.Action) {
			tests[key].PrintDetail(flags)
			printed[key] = true
		}
	}

	if len(tests) > 0 {
		if flags.Results.Any(StatusNone) {
			noneTests := tests.
				FilterKeys(printed).
				FilterAction(EndingActions...)
			for _, key := range noneTests.OrderedKeys() {
				tests[key].PrintDetail(flags)
				printed[key] = true
			}
		}

		// print summaries
		for _, status := range flags.Summary {
			if status == StatusNone {
				filtered := tests.FilterAction(EndingActions...)
				if len(filtered) > 0 {
					filtered.PrintSummary(status)
				}

			} else {

				for _, action := range EndingActions {
					if status.IsAction(action) {

						filtered := tests.FindByAction(action)

						if action == ActionSkip {
							if flags.V <= V3 {
								filtered = filtered.FilterNotests()
							}
						}

						if len(filtered) > 0 {
							filtered.PrintSummary(status)

						}

					}
				}
			}
		}

		if coverEnabled {
			filtered := tests.WithCoverage()
			if len(filtered) > 0 {
				filtered.PrintCoverage()
			}
		}

		{
			allFail := tests.FindByAction(ActionFail)
			allPass := tests.FindByAction(ActionPass)
			allSkip := tests.FindByAction(ActionSkip)
			allNone := tests.FilterAction(EndingActions...)

			countPass := allPass.CountTests()
			countFail := allFail.CountTests()
			countNone := len(allNone)
			countSkip := allSkip.CountTests()

			pass := statusNames[StatusPass] + ":" + fmt.Sprint(countPass)
			fail := statusNames[StatusFail] + ":" + fmt.Sprint(countFail)
			none := statusNames[StatusNone] + ":" + fmt.Sprint(countNone)
			skip := statusNames[StatusSkip] + ":" + fmt.Sprint(countSkip)

			statusColor := hardLineColor

			if countPass > 0 {
				statusColor = passColorBold
				pass = statusColor(pass)
			}

			if countNone > 0 {
				statusColor = noneColorBold
				none = statusColor(none)
			}

			if countFail > 0 {
				statusColor = failColorBold
				fail = statusColor(fail)
			}

			// if countSkip > 0 {
			// skip = skipColorBold(skip)
			// }

			fmt.Println("")
			sep := " " + statusColor("|") + " "
			status := statusColor("══════") + " " +
				statusColor(time.Now().Format("15:04:05")) +
				sep + pass +
				sep + fail +
				sep + none +
				sep + skip +
				sep + statusColor(time.Now().Sub(t0).Round(time.Millisecond).String()) +
				"  " + statusColor("══════")

			fmt.Println(status)

		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("error reading standard input:", err)

	}
	go stdout.Close()

	go func() {
		time.Sleep(2 * time.Second)
		cancel()
	}()
	cmdErr := cmd.Wait()
	var ee *exec.ExitError
	if cmdErr != nil && errors.As(cmdErr, &ee) {
		if ee.Exited() {
			return ExitError(ee.ExitCode())
		}
	}
	return nil
}
