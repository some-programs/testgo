package main

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
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

var (
	defaultColor  = color.New().SprintFunc()
	lineColor     = color.New().SprintFunc()
	hardLineColor = color.New().SprintFunc()
	packageColor  = color.New().SprintFunc()
	testColor     = color.New(color.FgMagenta).SprintFunc()
	timeColor     = color.New(color.FgCyan).SprintFunc()
	coverColor    = color.New(color.FgBlue).SprintFunc()
	failColor     = color.New(color.FgRed).SprintFunc()
	noneColor     = color.New(color.FgYellow).SprintFunc()
	passColor     = color.New(color.FgGreen).SprintFunc()
	skipColor     = color.New(color.FgHiMagenta).SprintFunc()

	statusColors = map[Status](func(a ...interface{}) string){
		StatusFail:  failColor,
		StatusPass:  passColor,
		StatusNone:  noneColor,
		StatusSkip:  skipColor,
		StatusBench: passColor,
	}
)
