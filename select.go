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
	conn   net.Conn
	up     bool
	err    error
	at     time.Time
}

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
			for {
				select {
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
		err:    fmt.Errorf("probe failed after %s: %w", time.Since(start).Round(time.Millisecond), err),
		at:     time.Now(),
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
