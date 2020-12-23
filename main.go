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
	"sync"
	"syscall"
	"time"

	"github.com/peterbourgon/ff/v3"
)

// Flags .
type Flags struct {
	V       Verbosity
	Config  string
	Results Statuses
	Summary Statuses
}

func (f *Flags) Register(fs *flag.FlagSet) {
	f.Results = []Status{StatusFail, StatusNone}
	f.Summary = []Status{StatusFail, StatusNone}

	fs.Var(&f.Results, "results", "types of results to show")
	fs.Var(&f.Summary, "summary", "types of summary to show")
	fs.IntVar((*int)(&f.V), "v", 0, "0(lowest) to 5(highest)")
	fs.StringVar(&f.Config, "config", "", "config file")
}

func (f *Flags) Setup() {
	if f.V >= 5 {
		f.Results = AllStatuses
		f.Summary = AllStatuses
	}
}

func main() {
	log.SetFlags(log.Lshortfile)

	fs := flag.NewFlagSet("runtests", flag.ExitOnError)
	var flags Flags
	flags.Register(fs)

	err := ff.Parse(fs, nil,
		ff.WithEnvVarPrefix("TGO"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
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

		tests := make(TestStorage, 0)
		printed := make(map[Key]bool, 0)
		scanner := bufio.NewScanner(stdout)

	scan:
		for scanner.Scan() {
			var e Event
			log.Println("SCANg LINE:", scanner.Text())
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				log.Println(err, scanner.Text())
				continue scan
			}
			tests.Append(e)
			key := e.Key()
			// fmt.Println("flags.Results", flags.Results)
			if !printed[key] && flags.Results.HasAction(e.Action) {
				tests[key].PrintDetail(flags.V)
				printed[key] = true
			}
		}

		if flags.Results.Any(StatusNone) {
			noneTests := tests.
				FilterKeys(printed).
				FilterAction(EndingActions...)
			for _, key := range noneTests.OrderedKeys() {
				tests[key].PrintDetail(flags.V)
				printed[key] = true
			}
		}

		// print summaries

		for _, status := range flags.Summary {
			if status == StatusNone {
				filtered := tests.
					FilterAction(EndingActions...)
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

			if countFail > 0 {
				fail = statusColors[StatusFail](fail)
			}
			if countNone > 0 {
				none = statusColors[StatusNone](none)
			}
			if countSkip > 0 {
				skip = statusColors[StatusSkip](skip)
			}

			fmt.Println("")
			sep := " " + lineColor("|") + " "
			fmt.Println(hardLineColor("══════") + " " +
				time.Now().Format("15:04:05") +
				sep + pass +
				sep + fail +
				sep + none +
				sep + skip +
				sep + time.Now().Sub(t0).Round(time.Millisecond).String() +
				"  " + hardLineColor("══════"))
			fmt.Println("")
		}

		if err := scanner.Err(); err != nil {
			fmt.Println("error reading standard input:", err)
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
