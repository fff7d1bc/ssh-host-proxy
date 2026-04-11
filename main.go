package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

var errUsageDisplayed = errors.New("usage displayed")

type target struct {
	raw string
}

type probeResult struct {
	target target
	up     bool
	err    error
	at     time.Time
}

var probeFunc = probeOnce

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, errUsageDisplayed) || errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	selected, states, err := waitForTarget(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if cfg.dryRun {
			printDryRun(nil, states)
		}
		os.Exit(1)
	}

	if cfg.dryRun {
		printDryRun(&selected, states)
		return
	}

	if err := proxyTo(selected.raw, cfg.connectTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "error: proxy to %s failed: %v\n", selected.raw, err)
		os.Exit(1)
	}
}

type config struct {
	targets           []target
	selectionInterval time.Duration
	connectTimeout    time.Duration
	dryRun            bool
}

func parseConfig(args []string) (config, error) {
	var targetsArg string
	var help bool
	var cfg config

	fs := flag.NewFlagSet("ssh-host-proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&targetsArg, "targets", "", "Comma-separated host:port targets in priority order")
	fs.DurationVar(&cfg.selectionInterval, "selection-interval", time.Second, "How often to probe targets and re-evaluate which ones are reachable")
	fs.DurationVar(&cfg.connectTimeout, "connect-timeout", 10*time.Second, "Maximum total time to keep probing before giving up")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "Print what would be selected and exit")
	fs.BoolVar(&help, "help", false, "Print help and exit")
	fs.BoolVar(&help, "h", false, "Print help and exit")
	fs.Usage = func() {
		printUsage(os.Stderr)
	}

	if len(args) == 0 {
		printUsage(os.Stderr)
		return cfg, errUsageDisplayed
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stderr)
		}
		return cfg, err
	}

	if help {
		printUsage(os.Stderr)
		return cfg, errUsageDisplayed
	}

	if strings.TrimSpace(targetsArg) == "" {
		return cfg, errors.New("--targets is required")
	}
	if cfg.selectionInterval <= 0 {
		return cfg, errors.New("--selection-interval must be greater than 0")
	}
	if cfg.connectTimeout <= 0 {
		return cfg, errors.New("--connect-timeout must be greater than 0")
	}

	targets, err := parseTargets(targetsArg)
	if err != nil {
		return cfg, err
	}
	cfg.targets = targets
	return cfg, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  ssh-host-proxy --targets host1:22,host2:22,host3:22 [--selection-interval 1s] [--connect-timeout 10s] [--dry-run]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --targets string")
	fmt.Fprintln(w, "        Comma-separated host:port targets in priority order")
	fmt.Fprintln(w, "  --selection-interval duration")
	fmt.Fprintln(w, "        How often to probe targets and re-evaluate which ones are reachable (default 1s)")
	fmt.Fprintln(w, "  --connect-timeout duration")
	fmt.Fprintln(w, "        Maximum total time to keep probing before giving up (default 10s)")
	fmt.Fprintln(w, "  --dry-run")
	fmt.Fprintln(w, "        Print what would be selected and exit")
	fmt.Fprintln(w, "  --help")
	fmt.Fprintln(w, "        Print help and exit")
}

func parseTargets(value string) ([]target, error) {
	parts := strings.Split(value, ",")
	targets := make([]target, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		raw := strings.TrimSpace(part)
		if raw == "" {
			return nil, errors.New("targets list contains an empty entry")
		}
		if _, _, err := net.SplitHostPort(raw); err != nil {
			return nil, fmt.Errorf("invalid target %q: %w", raw, err)
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		targets = append(targets, target{raw: raw})
	}

	if len(targets) == 0 {
		return nil, errors.New("no valid targets provided")
	}

	return targets, nil
}

func waitForTarget(cfg config) (target, map[string]probeResult, error) {
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
							return selected, states, nil
						}
						return target{}, states, errors.New("no reachable targets responded before --connect-timeout")
					}
					prev, exists := states[res.target.raw]
					if !exists || res.at.After(prev.at) || (res.at.Equal(prev.at) && res.up && !prev.up) {
						states[res.target.raw] = res
					}
				default:
					if selected, ok := firstReachable(cfg.targets, states); ok {
						cancel()
						return selected, states, nil
					}
					return target{}, states, errors.New("no reachable targets responded before --connect-timeout")
				}
			}
		case <-ticker.C:
			if selected, ok := firstReachable(cfg.targets, states); ok {
				cancel()
				return selected, states, nil
			}
		case res, ok := <-results:
			if !ok {
				if selected, ok := firstReachable(cfg.targets, states); ok {
					cancel()
					return selected, states, nil
				}
				return target{}, states, errors.New("no reachable targets responded before --connect-timeout")
			}
			prev, exists := states[res.target.raw]
			if !exists || res.at.After(prev.at) || (res.at.Equal(prev.at) && res.up && !prev.up) {
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
		_ = conn.Close()
		return probeResult{target: t, up: true, at: time.Now()}
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return probeResult{target: t, up: false, err: err, at: time.Now()}
	}

	// Keep the last failure for dry-run diagnostics.
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
			return t, true
		}
	}
	return target{}, false
}

func printDryRun(selected *target, states map[string]probeResult) {
	if selected != nil {
		fmt.Fprintf(os.Stderr, "selected target: %s\n", selected.raw)
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

func proxyTo(addr string, connectTimeout time.Duration) error {
	dialer := net.Dialer{Timeout: connectTimeout}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	copyErr := make(chan error, 2)

	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
		copyErr <- err
	}()

	go func() {
		_, err := io.Copy(os.Stdout, conn)
		copyErr <- err
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-copyErr
		if err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
