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
	defer cancel()

	states := make(map[string]probeResult, len(cfg.targets))
	results := make(chan probeResult, len(cfg.targets))
	var wg sync.WaitGroup

	for _, t := range cfg.targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			results <- probeFunc(ctx, t, cfg.connectTimeout)
		}(t)
	}

	go func() {
		wg.Wait()
		close(results)
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
			for {
				select {
				case res, ok := <-results:
					if !ok {
						if selected, ok := firstReachable(cfg.targets, states); ok {
							cancel()
							// Keep only the chosen live connection; everything else is
							// just probe state and should be torn down.
							closeUnusedConnections(states, selected.raw)
							return states[selected.raw], states, nil
						}
						closeAllConnections(states)
						return probeResult{}, states, errors.New("no reachable targets responded before --connect-timeout")
					}
					prev, exists := states[res.target.raw]
					if !exists || res.at.After(prev.at) || (res.at.Equal(prev.at) && res.up && !prev.up) {
						// If a newer result replaces an older successful one, close
						// the previous connection so only the latest candidate stays live.
						closeConn(prev.conn)
						states[res.target.raw] = res
					}
				default:
					if selected, ok := firstReachable(cfg.targets, states); ok {
						cancel()
						closeUnusedConnections(states, selected.raw)
						return states[selected.raw], states, nil
					}
					closeAllConnections(states)
					return probeResult{}, states, errors.New("no reachable targets responded before --connect-timeout")
				}
			}
		case <-ticker.C:
			// Selection happens on the interval tick so several targets can finish
			// racing in the same window and we still prefer by configured order,
			// not merely by whichever goroutine reported first.
			if selected, ok := firstReachable(cfg.targets, states); ok {
				cancel()
				closeUnusedConnections(states, selected.raw)
				return states[selected.raw], states, nil
			}
		case res, ok := <-results:
			if !ok {
				if selected, ok := firstReachable(cfg.targets, states); ok {
					cancel()
					closeUnusedConnections(states, selected.raw)
					return states[selected.raw], states, nil
				}
				closeAllConnections(states)
				return probeResult{}, states, errors.New("no reachable targets responded before --connect-timeout")
			}
			prev, exists := states[res.target.raw]
			if !exists || res.at.After(prev.at) || (res.at.Equal(prev.at) && res.up && !prev.up) {
				closeConn(prev.conn)
				states[res.target.raw] = res
			}
		}
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
