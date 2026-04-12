package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

var errUsageDisplayed = errors.New("usage displayed")

type config struct {
	targets           []target
	selectionInterval time.Duration
	connectTimeout    time.Duration
	fdpass            bool
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
	fs.BoolVar(&cfg.fdpass, "fdpass", false, "Pass the connected socket to ssh instead of proxying traffic in-process")
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
	fmt.Fprintln(w, "  ssh-host-proxy --targets host1:22,host2:22,host3:22 [--selection-interval 1s] [--connect-timeout 10s] [--fdpass] [--dry-run]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --targets string")
	fmt.Fprintln(w, "        Comma-separated host:port targets in priority order")
	fmt.Fprintln(w, "  --selection-interval duration")
	fmt.Fprintln(w, "        How often to probe targets and re-evaluate which ones are reachable (default 1s)")
	fmt.Fprintln(w, "  --connect-timeout duration")
	fmt.Fprintln(w, "        Maximum total time to keep probing before giving up (default 10s)")
	fmt.Fprintln(w, "  --fdpass")
	fmt.Fprintln(w, "        Pass the connected socket to ssh instead of proxying traffic in-process")
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
