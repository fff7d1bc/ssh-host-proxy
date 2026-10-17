package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

type target struct {
	raw string
}

type probeResult struct {
	target target
	// conn is kept open for the winning target so the real SSH session can
	// reuse the already-established connection instead of dialing again.
	conn net.Conn
	up   bool
	err  error
	at   time.Time
}

// probeFunc is injectable for tests so selection behavior can be exercised
// deterministically without real network timing.
var probeFunc = probeOnce

func waitForTarget(cfg config) (probeResult, map[string]probeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.connectTimeout)

	states := make(map[string]probeResult, len(cfg.targets))
	results := make(chan probeResult, len(cfg.targets))
	probe := probeFunc
	var wg sync.WaitGroup

	for _, t := range cfg.targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			results <- probe(ctx, t, cfg.connectTimeout)
		}(t)
	}

	go func() {
		wg.Wait()
		close(results)
	}()
	defer func() {
		cancel()
		// Selection must not wait for every canceled dial to unwind, but any
		// connection returned after this function exits still belongs to us.
		go closeRemainingProbeConnections(results)
	}()

	ticker := time.NewTicker(cfg.selectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Enforce a hard overall timeout: drain only results that are already
			// available right now, then return. Waiting for every goroutine to
			// unwind here would make the timeout effectively "connect-timeout plus
			// cleanup time".
			drainReadyProbeResults(results, states)
			if selected, ok := firstReachable(cfg.targets, states); ok {
				cancel()
				// Keep only the chosen live connection; everything else is
				// just probe state and should be torn down.
				closeUnusedConnections(states, selected.raw)
				return states[selected.raw], states, nil
			}
			closeAllConnections(states)
			return probeResult{}, states, selectionFailure(ctx)
		case <-ticker.C:
			// If higher-priority probes are still pending, the interval bounds
			// how long we wait before using an already-successful fallback.
			// Drain queued results to prefer configured order at this point.
			closed := drainReadyProbeResults(results, states)
			if selected, ok := firstReachable(cfg.targets, states); ok {
				cancel()
				closeUnusedConnections(states, selected.raw)
				return states[selected.raw], states, nil
			}
			if closed {
				closeAllConnections(states)
				return probeResult{}, states, selectionFailure(ctx)
			}
		case res, ok := <-results:
			if !ok {
				if selected, ok := firstReachable(cfg.targets, states); ok {
					cancel()
					closeUnusedConnections(states, selected.raw)
					return states[selected.raw], states, nil
				}
				closeAllConnections(states)
				return probeResult{}, states, selectionFailure(ctx)
			}
			recordProbeResult(states, res)
			if selected, ok := settledReachable(cfg.targets, states); ok {
				cancel()
				closeUnusedConnections(states, selected.raw)
				return states[selected.raw], states, nil
			}
		}
	}
}

func selectionFailure(ctx context.Context) error {
	// Channel closure and the deadline may become ready together. Use the
	// context state so the diagnostic does not depend on which branch ran.
	if ctx.Err() == context.DeadlineExceeded {
		return errors.New("no reachable targets responded before --connect-timeout")
	}
	return errors.New("all target connection attempts failed")
}

func closeRemainingProbeConnections(results <-chan probeResult) {
	for res := range results {
		closeConn(res.conn)
	}
}

func drainReadyProbeResults(results <-chan probeResult, states map[string]probeResult) bool {
	for {
		select {
		case res, ok := <-results:
			if !ok {
				return true
			}
			recordProbeResult(states, res)
		default:
			return false
		}
	}
}

func recordProbeResult(states map[string]probeResult, res probeResult) {
	prev, exists := states[res.target.raw]
	if !exists || res.at.After(prev.at) || (res.at.Equal(prev.at) && res.up && !prev.up) {
		// If a newer result replaces an older successful one, close the previous
		// connection so only the latest candidate stays live.
		closeConn(prev.conn)
		states[res.target.raw] = res
	}
}

func probeOnce(ctx context.Context, t target, connectTimeout time.Duration) probeResult {
	start := time.Now()
	dialer := net.Dialer{Timeout: connectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", t.raw)
	if err == nil {
		return probeResult{target: t, conn: conn, up: true, at: time.Now()}
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return probeResult{target: t, up: false, err: err, at: time.Now()}
	}

	return probeResult{
		target: t,
		up:     false,
		// Keep the last dial failure for dry-run diagnostics.
		err: fmt.Errorf("probe failed after %s: %w", time.Since(start).Round(time.Millisecond), err),
		at:  time.Now(),
	}
}

func firstReachable(targets []target, states map[string]probeResult) (target, bool) {
	for _, t := range targets {
		state, ok := states[t.raw]
		if ok && state.up {
			return state.target, true
		}
	}
	return target{}, false
}

func settledReachable(targets []target, states map[string]probeResult) (target, bool) {
	// Each target is probed once. Once every higher-priority target has
	// failed, no pending result can improve on this successful connection.
	for _, t := range targets {
		state, ok := states[t.raw]
		if !ok {
			return target{}, false
		}
		if state.up {
			return t, true
		}
	}
	return target{}, false
}

func printDryRun(selected *probeResult, states map[string]probeResult) {
	if selected != nil {
		fmt.Fprintf(os.Stderr, "selected target: %s\n", selected.target.raw)
	} else {
		fmt.Fprintln(os.Stderr, "selected target: none")
	}

	for _, state := range sortStates(states) {
		status := "offline"
		if state.up {
			status = "online"
		}

		if state.err != nil {
			fmt.Fprintf(os.Stderr, "%s %s (%v)\n", status, state.target.raw, state.err)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", status, state.target.raw)
	}
}

func sortStates(states map[string]probeResult) []probeResult {
	// Dry-run output is debug-oriented, so keep the order deterministic.
	results := make([]probeResult, 0, len(states))
	for _, state := range states {
		results = append(results, state)
	}

	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].target.raw < results[i].target.raw {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results
}
