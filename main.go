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
		log.Println("SCANg LINE:", scanner.Text())
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			log.Println(err, scanner.Text())
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

type ExitError int

func (e ExitError) Error() string {
	return strconv.FormatInt(int64(e), 10)
}
