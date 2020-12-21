package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
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

// Flags .
type Flags struct {
	ShowSkipped bool
	ShowNone    bool
	ShowFailed  bool
	ShowPass    bool
	ShowAll     bool
	V           Verbosity
	Config      string
}

func (f *Flags) Register(fs *flag.FlagSet) {
	fs.BoolVar(&f.ShowSkipped, "show.skipped", false, "")
	fs.BoolVar(&f.ShowNone, "show.none", true, "")
	fs.BoolVar(&f.ShowFailed, "show.failed", true, "")
	fs.BoolVar(&f.ShowPass, "sSHhow.pass", false, "")
	fs.BoolVar(&f.ShowAll, "show.all", false, "")
	fs.IntVar((*int)(&f.V), "verbose", 0, "0(lowest) to 5(highest)")
	fs.StringVar(&f.Config, "config", "", "config file")
}

func (f *Flags) Setup() {
	if f.V >= 5 {
		f.ShowAll = true
	}
	if f.ShowAll {
		f.ShowFailed = true
		f.ShowSkipped = true
		f.ShowNone = true
		f.ShowPass = true
	}
}

func main() {
	log.SetFlags(log.Lshortfile)

	fs := flag.NewFlagSet("runtests", flag.ExitOnError)
	var flags Flags
	flags.Register(fs)

	err := ff.Parse(fs, nil,
		ff.WithEnvVarPrefix("TG"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
	)
	if err != nil {
		fmt.Println("error", err)
	}

	flags.Setup()
	if flags.V <= V3 {
		log.SetOutput(ioutil.Discard)
	}

	log.Printf("flags %+v", flags)
	log.Printf("args: %+v", os.Args)

	ctx, cancel := context.WithCancel(context.Background())

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
		case <-ctx.Done():
			return
		}
	}()

	if err := run(ctx, flags, os.Args[1:]); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, flags Flags, argv []string) error {

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{"test", "-json"}
	args = append(args, argv...)
	log.Println("args", args)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Stderr = os.Stderr
	var wg sync.WaitGroup

	var coverEnabled bool
	for _, v := range argv {
		if v == "-cover" {
			coverEnabled = true
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Println(err)
		cancel()
	}

	doneC := make(chan bool, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		t0 := time.Now()

		tests := make(Tests, 0)
		printed := make(map[Key]bool, 0)
		scanner := bufio.NewScanner(stdout)

	scan:
		for scanner.Scan() {
			var e Event
			log.Println("SCAN LINE:", scanner.Text())
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				log.Println(err, scanner.Text())
				continue scan
			}
			tests.Append(e)

			if e.Action == ActionFail && e.Test == "" {
				packageTests := tests.
					FindPackageTestsByName(e.Package).
					FilterKeys(printed).
					FilterPassed()

				for _, key := range packageTests.OrderedKeys() {
					events := tests[key]
					events.PrintDetail(flags.V)
					printed[key] = true

				}

			} else if e.Action == ActionFail && e.Test != "" {
				key := e.Key()
				events := tests[key]
				events.PrintDetail(flags.V)
				printed[key] = true
			}
		}

		if flags.V >= V1 {
			restTests := tests.FilterKeys(printed)
			for _, key := range restTests.OrderedKeys() {
				events := tests[key]
				events.PrintDetail(flags.V)
				printed[key] = true
			}
		}

		passTests := tests.
			FindByAction(ActionPass)
		if flags.ShowPass {
			if len(passTests) > 0 {
				passTests.PrintSummary(&ActionPass, &StatusPass)
			}
		} else if coverEnabled {

			coverResults := passTests.
				FindAllPackageTests()

			if len(coverResults) > 0 {
				coverResults.PrintSummary(&ActionPass, &StatusPass)
			}

		}

		skipTests := tests.FindByAction(ActionSkip)
		if flags.ShowSkipped {
			if len(skipTests) > 0 {
				skipTests.PrintSummary(&ActionSkip, &StatusSkip)
			}
		}

		filteredTests := tests.
			FilterAction(ActionPass).
			FilterAction(ActionSkip)

		noneTests := filteredTests.FindWithoutAction(ActionFail, ActionPass, ActionSkip)
		if flags.ShowNone {
			if len(noneTests) > 0 {
				noneTests.PrintSummary(nil, &StatusNone)
			}
		}

		failTests := filteredTests.FindByAction(ActionFail)
		if flags.ShowFailed {
			if len(failTests) > 0 {
				failTests.PrintSummary(&ActionFail, &StatusFail)
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "reading standard input:", err)
		}

		{
			var length int
			pass := statusNames[StatusPass] + ":" + fmt.Sprint(len(passTests))
			fail := statusNames[StatusFail] + ":" + fmt.Sprint(len(failTests))
			none := statusNames[StatusNone] + ":" + fmt.Sprint(len(noneTests))
			skip := statusNames[StatusSkip] + ":" + fmt.Sprint(len(skipTests))

			length += len(pass) + len(fail) + len(none) + len(skip)

			if len(failTests) > 0 {
				fail = statusColors[StatusFail](fail)
			}
			if len(noneTests) > 0 {
				none = statusColors[StatusNone](none)
			}
			if len(skipTests) > 0 {
				skip = statusColors[StatusSkip](skip)
			}

			fmt.Println("")
			sep := " " + lineColor("|") + " "
			fmt.Println(testColor("   ═══   ") + lineColor("[ ") +
				time.Now().Format("15:04:05") +
				sep + pass +
				sep + fail +
				sep + none +
				sep + skip +
				sep + time.Now().Sub(t0).Round(time.Millisecond).String() +
				lineColor(" ]") + testColor("   ═══   "))
			fmt.Println("")
		}

		doneC <- true
	}()

	if err := cmd.Start(); err != nil {
		log.Println(err)
	}

	go func() {
		wg.Wait()
		doneC <- true
	}()

	select {
	case ok := <-doneC:
		cancel()
		err := cmd.Wait()
		if ok {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

}

var (
	lineColor    = color.New(color.FgGreen).SprintFunc()
	failColor    = color.New(color.FgRed).SprintFunc()
	warnColor    = color.New(color.FgHiMagenta).SprintFunc()
	passColor    = color.New(color.FgGreen).SprintFunc()
	packageColor = color.New(color.FgBlue).SprintFunc()
	testColor    = color.New(color.FgMagenta).SprintFunc()
	skipColor    = color.New().SprintFunc()
	timeColor    = color.New(color.FgCyan).SprintFunc()

	statusColors = map[Status](func(a ...interface{}) string){
		StatusFail: failColor,
		StatusPass: passColor,
		StatusNone: warnColor,
		StatusSkip: skipColor,
	}
)

type Action string

var (
	ActionRun    = Action("run")
	ActionPause  = Action("pause")
	ActionCont   = Action("cont")
	ActionPass   = Action("pass")
	ActionBench  = Action("bench")
	ActionFail   = Action("fail")
	ActionOutput = Action("output")
	ActionSkip   = Action("skip")
)

type Status string

var (
	StatusPass = Status("PASS")
	StatusFail = Status("FAIL")
	StatusSkip = Status("SKIP")
	StatusNone = Status("NONE")

	statusNames = map[Status]string{
		StatusFail: "FAIL",
		StatusPass: "PASS",
		StatusNone: "NONE",
		StatusSkip: "SKIP",
	}
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
		if e.Action == ActionFail {
			return StatusFail
		}
		if e.Action == ActionPass {
			return StatusPass
		}
		if e.Action == ActionSkip {
			return StatusSkip
		}
	}
	return StatusNone
}

func (es Events) FindFirstByAction(action Action) *Event {
	for _, v := range es {
		if v.Action == action {
			return &v
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
					(strings.HasPrefix(outputWS, "coverage:") && strings.HasSuffix(outputWS, "of statements")))) {
			continue loop
		}
		v = append(v, e)
	}
	return v
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
	}
	if event == nil {
		event = &events[0]
	}
	var testName string
	if event.Test != "" {
		testName = "." + testColor(event.Test)
	}
	var elapsed string
	if event.Elapsed >= 0.01 {
		elapsed = " " + timeColor(fmt.Sprintf("(%.2fs)", event.Elapsed))
	}
	coverage := es.FindCoverage()
	if len(coverage) > 0 {
		coverage = fmt.Sprintf(" {%s} ", coverage)
	}

	statusColor := statusColors[status]
	fmt.Print(lineColor("═══") +
		" " + statusColor(statusNames[status]) +
		" " + statusColor(event.Package) + testName +
		elapsed + coverage +
		"\n",
	)

loop:
	for _, e := range events {
		if V <= V3 && e.Output == "" {
			continue loop
		}
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
}

type Tests map[Key]Events

func (ts Tests) OrderedKeys() []Key {
	var tks []Key
	for k, _ := range ts {
		tks = append(tks, k)
	}
	sort.SliceStable(tks, func(i, j int) bool {
		return natural.Less(tks[i].String(), tks[j].String())
	})
	return tks
}

// Append event into tests
func (ts Tests) Append(e Event) {
	key := e.Key()
	events, _ := ts[key]
	events = append(events, e)
	ts[key] = events
}

func (ts Tests) Union(values ...Tests) Tests {
	tests := make(Tests, 0)
	for _, values := range values {
		for k, v := range values {
			tests[k] = v
		}
	}
	return tests
}

// FindPackageTestsByName returns all Tests belonging to a specific package.
func (ts Tests) FindPackageTestsByName(pkg string) Tests {
	tests := make(Tests, 0)
loop:
	for key, events := range ts {
		for _, e := range events {
			if e.Package == pkg {
				tests[key] = events
				continue loop
			}
		}
	}
	return tests
}

func (ts Tests) FindAllPackageTests() Tests {
	tests := make(Tests, 0)
	for key, events := range ts {
		if key.Package != "" && key.Test == "" {
			tests[key] = events
		}
	}
	return tests
}

func (ts Tests) FilterKeys(exclude map[Key]bool) Tests {
	tests := make(Tests, 0)
loop:
	for key, events := range ts {
		if !exclude[key] {
			tests[key] = events
			continue loop
		}
	}
	return tests
}

func (ts Tests) FilterPassed() Tests {
	tests := make(Tests, 0)
loop:
	for test, events := range ts {
		if events.FindFirstByAction(ActionPass) != nil {
			continue loop
		}
		tests[test] = events
	}
	return tests
}

func (ts Tests) FindByAction(action Action) Tests {
	tests := make(Tests, 0)
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

// Where tasks is none of
func (ts Tests) FindWithoutAction(actions ...Action) Tests {
	actionMatch := make(map[Action]bool, len(actions))
	for _, action := range actions {
		actionMatch[action] = true
	}
	tests := make(Tests, 0)
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

func (ts Tests) FilterAction(action Action) Tests {
	tests := make(Tests, 0)
	for key, events := range ts {
		var found bool
		for _, e := range events {
			if e.Action == action {
				found = true
			}
		}
		if !found {
			tests[key] = events
		}
	}
	return tests
}

func (ts Tests) PrintSummary(action *Action, status *Status) {
	hr := lineColor("════════════")
	var header string
	if status != nil {
		statusColor := statusColors[*status]
		header = statusColor(statusNames[*status])
		hr = statusColor("════════════")
	}

	fmt.Println(hr, header, fmt.Sprintf("[%v]", len(ts)))
	for _, key := range ts.OrderedKeys() {
		events := ts[key]
		var elapsed string
		if action != nil && (action == &ActionPass || action == &ActionFail || action == &ActionSkip) {
			fe := events.FindFirstByAction(*action)
			if fe != nil {
				elapsed = timeColor(fmt.Sprintf("(%.2fs)", fe.Elapsed))
			}
		}

		if key.Test == "" {
			coverage := events.FindCoverage()
			if len(coverage) > 0 {
				coverage = fmt.Sprintf(" {%s} ", coverage)
			}
			fmt.Print(packageColor(key.Package) +
				" " + timeColor(elapsed) +
				coverage +
				"\n",
			)
		} else {
			fmt.Print(packageColor(key.Package) +
				"." + testColor(key.Test) +
				" " + timeColor(elapsed) +
				"\n",
			)
		}
	}
}
