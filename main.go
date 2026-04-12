package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

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
		closeConn(selected.conn)
		printDryRun(&selected, states)
		return
	}

	if cfg.fdpass {
		if err := passConn(selected.conn); err != nil {
			closeConn(selected.conn)
			fmt.Fprintf(os.Stderr, "error: fdpass to %s failed: %v\n", selected.target.raw, err)
			os.Exit(1)
		}
		return
	}

	if err := proxyConn(selected.conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: proxy to %s failed: %v\n", selected.target.raw, err)
		os.Exit(1)
	}
}
