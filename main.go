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
	"strconv"
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
	Bin     string
}

func (f *Flags) Register(fs *flag.FlagSet) {
	f.Results = []Status{StatusFail, StatusNone}
	f.Summary = []Status{StatusFail, StatusNone}

	fs.StringVar(&f.Bin, "bin", "go", "go binary name")
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

	flags.Setup()
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

	var coverEnabled bool
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
		if !printed[key] && flags.Results.HasAction(e.Action) {
			tests[key].PrintDetail(flags.V)
			printed[key] = true
		}
	}

	if len(tests) > 0 {
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

type ExitError int

func (e ExitError) Error() string {
	return strconv.FormatInt(int64(e), 10)
}
