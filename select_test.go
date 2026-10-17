package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestWaitForTargetReportsCompletedFailures(t *testing.T) {
	originalProbe := probeFunc
	t.Cleanup(func() { probeFunc = originalProbe })
	probeFunc = func(_ context.Context, tg target, _ time.Duration) probeResult {
		return probeResult{target: tg, err: errors.New("connection refused")}
	}
	start := time.Now()
	_, states, err := waitForTarget(config{
		targets:           []target{{raw: "first:22"}, {raw: "second:22"}},
		selectionInterval: time.Second, connectTimeout: 2 * time.Second,
	})
	if err == nil || err.Error() != "all target connection attempts failed" {
		t.Fatalf("unexpected completed-probe diagnostic: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("completed failures waited for interval or deadline: %v", elapsed)
	}
	if len(states) != 2 {
		t.Fatalf("expected both probe failures, got %v", states)
	}
}

func TestSelectionFailureUsesContextDeadline(t *testing.T) {
	// A results channel can close at the deadline. The same context state
	// must produce the timeout diagnostic regardless of which event won.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if got := selectionFailure(ctx).Error(); got != "no reachable targets responded before --connect-timeout" {
		t.Fatalf("unexpected expired-context diagnostic: %s", got)
	}
	if got := selectionFailure(context.Background()).Error(); got != "all target connection attempts failed" {
		t.Fatalf("unexpected live-context diagnostic: %s", got)
	}
}

func TestPrintDryRunOrdersFailureDiagnosticsOnStderr(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	originalStderr := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = originalStderr }()
	printDryRun(nil, map[string]probeResult{
		"z:22": {target: target{raw: "z:22"}, err: errors.New("connection refused")},
		"a:22": {target: target{raw: "a:22"}, err: context.DeadlineExceeded},
	})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	want := "selected target: none\noffline a:22 (context deadline exceeded)\noffline z:22 (connection refused)\n"
	if string(output) != want {
		t.Fatalf("unexpected dry-run stderr: %q, want %q", output, want)
	}
}

func TestSettledReachable(t *testing.T) {
	targets := []target{{raw: "preferred:22"}, {raw: "fallback:22"}, {raw: "last:22"}}
	for _, tc := range []struct {
		name    string
		results []probeResult
		want    string
	}{
		{name: "nothing completed"},
		{name: "first target succeeds", results: []probeResult{{target: targets[0], up: true}}, want: targets[0].raw},
		{name: "higher priority pending", results: []probeResult{{target: targets[1], up: true}}},
		{name: "failure then success", results: []probeResult{{target: targets[0]}, {target: targets[1], up: true}}, want: targets[1].raw},
		{name: "success then failure", results: []probeResult{{target: targets[1], up: true}, {target: targets[0]}}, want: targets[1].raw},
		{name: "pending between failure and success", results: []probeResult{{target: targets[0]}, {target: targets[2], up: true}}},
		{name: "two successes", results: []probeResult{{target: targets[1], up: true}, {target: targets[0], up: true}}, want: targets[0].raw},
		{name: "all failed", results: []probeResult{{target: targets[0]}, {target: targets[1]}, {target: targets[2]}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			states := make(map[string]probeResult)
			for i, res := range tc.results {
				recordProbeResult(states, res)
				got, ok := settledReachable(targets, states)
				if i < len(tc.results)-1 && ok {
					t.Fatalf("selected %s before higher-priority result arrived", got.raw)
				}
			}
			got, ok := settledReachable(targets, states)
			if got.raw != tc.want || ok != (tc.want != "") {
				t.Fatalf("got (%q, %v), want %q", got.raw, ok, tc.want)
			}
		})
	}
}

func TestWaitForTargetSelectsSettledWinnerBeforeInterval(t *testing.T) {
	for _, failedPreferred := range []bool{false, true} {
		name := "first target succeeds"
		if failedPreferred {
			name = "higher priority fails"
		}
		t.Run(name, func(t *testing.T) {
			originalProbe := probeFunc
			t.Cleanup(func() { probeFunc = originalProbe })
			winner, late := &fakeConn{}, &fakeConn{}
			t.Cleanup(func() { closeConn(winner) })
			probeFunc = func(ctx context.Context, tg target, _ time.Duration) probeResult {
				switch tg.raw {
				case "failed:22":
					return probeResult{target: tg}
				case "winner:22":
					return probeResult{target: tg, up: true, conn: winner}
				default:
					<-ctx.Done()
					return probeResult{target: tg, up: true, conn: late}
				}
			}
			targets := []target{{raw: "winner:22"}, {raw: "blocked:22"}}
			if failedPreferred {
				targets = append([]target{{raw: "failed:22"}}, targets...)
			}
			start := time.Now()
			selected, _, err := waitForTarget(config{
				targets: targets, selectionInterval: time.Second, connectTimeout: 2 * time.Second,
			})
			elapsed := time.Since(start)
			if err != nil || selected.conn != winner {
				t.Fatalf("expected winning connection, got %v, %v", selected, err)
			}
			if elapsed >= 500*time.Millisecond {
				t.Fatalf("selection waited for interval: %v", elapsed)
			}
			t.Logf("settled selection took %v with a 1s interval", elapsed)
			if winner.closed.Load() != 0 {
				t.Fatal("winning connection was closed")
			}
			waitForClosedConn(t, late)
		})
	}
}

func TestWaitForTargetWaitsForIntervalWithPreferredProbePending(t *testing.T) {
	originalProbe := probeFunc
	t.Cleanup(func() { probeFunc = originalProbe })
	winner := &fakeConn{}
	t.Cleanup(func() { closeConn(winner) })
	probeFunc = func(ctx context.Context, tg target, _ time.Duration) probeResult {
		if tg.raw == "preferred:22" {
			<-ctx.Done()
			return probeResult{target: tg, err: ctx.Err()}
		}
		return probeResult{target: tg, up: true, conn: winner}
	}
	const interval = 40 * time.Millisecond
	start := time.Now()
	selected, _, err := waitForTarget(config{
		targets:           []target{{raw: "preferred:22"}, {raw: "fallback:22"}},
		selectionInterval: interval, connectTimeout: time.Second,
	})
	if err != nil || selected.conn != winner {
		t.Fatalf("expected fallback connection, got %v, %v", selected, err)
	}
	if elapsed := time.Since(start); elapsed < interval {
		t.Fatalf("selected before interval with higher priority pending: %v", elapsed)
	}
}
