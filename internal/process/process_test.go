package process

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesBoundedOutputAndStructuredResult(t *testing.T) {
	runner := helperRunner(5, time.Second)
	got := runner.Run(context.Background(), os.Args[0], "-test.run=TestHelperProcess", "--", "output")
	if got.Err != nil || got.ExitCode != 0 || got.Duration <= 0 || !got.StdoutTruncated || !got.StderrTruncated {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.Stdout) != 5 || len(got.Stderr) != 5 {
		t.Fatalf("output limits not enforced: %+v", got)
	}
}

func TestRunnerRejectsMissingExecutable(t *testing.T) {
	got := Runner{}.Run(context.Background(), "definitely-not-an-executable", "x")
	if got.Err == nil || got.ExitCode != -1 {
		t.Fatalf("expected structured error: %+v", got)
	}
}

func TestRunnerTimeout(t *testing.T) {
	got := helperRunner(DefaultMaxOutput, 20*time.Millisecond).Run(context.Background(), os.Args[0], "-test.run=TestHelperProcess", "--", "sleep")
	if !got.TimedOut || got.Err == nil {
		t.Fatalf("expected timeout: %+v", got)
	}
}

func TestRunnerArgvNotShell(t *testing.T) {
	got := Runner{}.Run(context.Background(), "go", "env", "A; echo injected")
	if strings.Contains(got.Stdout, "injected") {
		t.Fatal("shell injection")
	}
}

func TestRunnerCancellationAndNonzeroExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := helperRunner(DefaultMaxOutput, time.Second).Run(ctx, os.Args[0], "-test.run=TestHelperProcess", "--", "sleep")
	if !cancelled.Cancelled || cancelled.Err == nil {
		t.Fatalf("expected cancellation: %+v", cancelled)
	}

	failed := helperRunner(DefaultMaxOutput, time.Second).Run(context.Background(), os.Args[0], "-test.run=TestHelperProcess", "--", "fail")
	if failed.ExitCode != 7 || failed.Err == nil {
		t.Fatalf("expected exit 7: %+v", failed)
	}
}

func TestRunnerEnvironmentOverlay(t *testing.T) {
	t.Setenv("EXTENT_PROCESS_TEST", "original")
	runner := helperRunner(DefaultMaxOutput, time.Second)
	runner.Env = append(runner.Env, "EXTENT_PROCESS_TEST=overridden")
	got := runner.Run(context.Background(), os.Args[0], "-test.run=TestHelperProcess", "--", "env")
	if got.Err != nil || strings.TrimSpace(got.Stdout) != "overridden" {
		t.Fatalf("environment overlay failed: %+v", got)
	}
}

func helperRunner(maxOutput int, timeout time.Duration) Runner {
	return Runner{MaxOutput: maxOutput, Timeout: timeout, Env: []string{"GO_WANT_EXTENT_HELPER_PROCESS=1"}}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXTENT_HELPER_PROCESS") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	switch os.Args[separator+1] {
	case "output":
		fmt.Fprint(os.Stdout, "0123456789")
		fmt.Fprint(os.Stderr, "abcdefghij")
	case "sleep":
		time.Sleep(10 * time.Second)
	case "fail":
		os.Exit(7)
	case "env":
		fmt.Fprint(os.Stdout, os.Getenv("EXTENT_PROCESS_TEST"))
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
