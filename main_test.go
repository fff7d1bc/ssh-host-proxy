package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestParseTargets(t *testing.T) {
	targets, err := parseTargets("192.168.1.10:22, example.local:2222,192.168.1.10:22")
	if err != nil {
		t.Fatalf("parseTargets returned error: %v", err)
	}

	if len(targets) != 2 {
		t.Fatalf("expected 2 deduplicated targets, got %d", len(targets))
	}

	if targets[0].raw != "192.168.1.10:22" {
		t.Fatalf("unexpected first target: %q", targets[0].raw)
	}

	if targets[1].raw != "example.local:2222" {
		t.Fatalf("unexpected second target: %q", targets[1].raw)
	}
}

func TestParseTargetsRejectsInvalidEntry(t *testing.T) {
	if _, err := parseTargets("192.168.1.10,example.local:22"); err == nil {
		t.Fatal("expected invalid target error, got nil")
	}
}

func TestFirstReachablePrefersConfiguredOrder(t *testing.T) {
	targets := []target{
		{raw: "wired.local:22"},
		{raw: "wifi.local:22"},
		{raw: "vpn.local:22"},
	}

	states := map[string]probeResult{
		"wifi.local:22":  {target: targets[1], up: true, at: time.Now()},
		"vpn.local:22":   {target: targets[2], up: true, at: time.Now()},
		"wired.local:22": {target: targets[0], up: false, at: time.Now()},
	}

	selected, ok := firstReachable(targets, states)
	if !ok {
		t.Fatal("expected reachable target, got none")
	}

	if selected.raw != "wifi.local:22" {
		t.Fatalf("expected wifi.local:22, got %q", selected.raw)
	}
}

func TestParseConfigAcceptsSelectionIntervalShorterThanTimeout(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--targets", "example.local:22",
		"--selection-interval", "1s",
		"--connect-timeout", "10s",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.selectionInterval != time.Second {
		t.Fatalf("unexpected selection interval: %v", cfg.selectionInterval)
	}

	if cfg.connectTimeout != 10*time.Second {
		t.Fatalf("unexpected connect timeout: %v", cfg.connectTimeout)
	}
}

func TestWaitForTargetContinuesPastFirstSelectionInterval(t *testing.T) {
	origProbeFunc := probeFunc
	t.Cleanup(func() {
		probeFunc = origProbeFunc
	})

	probeFunc = func(ctx context.Context, t target, _ time.Duration) probeResult {
		if t.raw == "later:22" {
			time.Sleep(25 * time.Millisecond)
			return probeResult{target: t, up: true, at: time.Now()}
		}

		<-ctx.Done()
		return probeResult{target: t, up: false, err: ctx.Err(), at: time.Now()}
	}

	cfg := config{
		targets: []target{
			{raw: "first:22"},
			{raw: "later:22"},
		},
		selectionInterval: 10 * time.Millisecond,
		connectTimeout:    100 * time.Millisecond,
	}

	start := time.Now()
	selected, _, err := waitForTarget(cfg)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("waitForTarget returned error: %v", err)
	}

	if selected.raw != "later:22" {
		t.Fatalf("expected later:22, got %q", selected.raw)
	}

	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected selection after at least one extra interval, got %v", elapsed)
	}
}

func TestWaitForTargetPrefersConfiguredOrderAmongSuccessfulTargets(t *testing.T) {
	origProbeFunc := probeFunc
	t.Cleanup(func() {
		probeFunc = origProbeFunc
	})

	probeFunc = func(ctx context.Context, t target, _ time.Duration) probeResult {
		switch t.raw {
		case "preferred:22":
			time.Sleep(15 * time.Millisecond)
			return probeResult{target: t, up: true, at: time.Now()}
		case "faster:22":
			time.Sleep(5 * time.Millisecond)
			return probeResult{target: t, up: true, at: time.Now()}
		default:
			<-ctx.Done()
			return probeResult{target: t, up: false, err: ctx.Err(), at: time.Now()}
		}
	}

	cfg := config{
		targets: []target{
			{raw: "preferred:22"},
			{raw: "faster:22"},
		},
		selectionInterval: 20 * time.Millisecond,
		connectTimeout:    100 * time.Millisecond,
	}

	selected, _, err := waitForTarget(cfg)
	if err != nil {
		t.Fatalf("waitForTarget returned error: %v", err)
	}

	if selected.raw != "preferred:22" {
		t.Fatalf("expected preferred:22, got %q", selected.raw)
	}
}

func TestWaitForTargetReturnsErrorWhenNothingResponds(t *testing.T) {
	origProbeFunc := probeFunc
	t.Cleanup(func() {
		probeFunc = origProbeFunc
	})

	probeFunc = func(ctx context.Context, t target, _ time.Duration) probeResult {
		<-ctx.Done()
		return probeResult{target: t, up: false, err: ctx.Err(), at: time.Now()}
	}

	cfg := config{
		targets: []target{
			{raw: "first:22"},
			{raw: "second:22"},
		},
		selectionInterval: 10 * time.Millisecond,
		connectTimeout:    40 * time.Millisecond,
	}

	_, _, err := waitForTarget(cfg)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitForTargetCancelsLosingProbeAfterSelection(t *testing.T) {
	origProbeFunc := probeFunc
	t.Cleanup(func() {
		probeFunc = origProbeFunc
	})

	canceled := make(chan struct{}, 1)
	var once sync.Once

	probeFunc = func(ctx context.Context, t target, _ time.Duration) probeResult {
		if t.raw == "winner:22" {
			time.Sleep(15 * time.Millisecond)
			return probeResult{target: t, up: true, at: time.Now()}
		}

		<-ctx.Done()
		once.Do(func() {
			close(canceled)
		})
		return probeResult{target: t, up: false, err: ctx.Err(), at: time.Now()}
	}

	cfg := config{
		targets: []target{
			{raw: "winner:22"},
			{raw: "loser:22"},
		},
		selectionInterval: 10 * time.Millisecond,
		connectTimeout:    100 * time.Millisecond,
	}

	selected, _, err := waitForTarget(cfg)
	if err != nil {
		t.Fatalf("waitForTarget returned error: %v", err)
	}

	if selected.raw != "winner:22" {
		t.Fatalf("expected winner:22, got %q", selected.raw)
	}

	select {
	case <-canceled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected losing probe to be canceled after selection")
	}
}

func TestWaitForTargetHonorsHardConnectTimeout(t *testing.T) {
	origProbeFunc := probeFunc
	t.Cleanup(func() {
		probeFunc = origProbeFunc
	})

	blocked := make(chan struct{})
	probeFunc = func(context.Context, target, time.Duration) probeResult {
		<-blocked
		return probeResult{}
	}

	cfg := config{
		targets: []target{
			{raw: "slow:22"},
		},
		selectionInterval: 10 * time.Millisecond,
		connectTimeout:    40 * time.Millisecond,
	}

	start := time.Now()
	_, _, err := waitForTarget(cfg)
	elapsed := time.Since(start)
	close(blocked)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected hard timeout near 40ms, got %v", elapsed)
	}
}
