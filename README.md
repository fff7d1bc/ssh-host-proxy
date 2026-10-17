# ssh-host-proxy

`ssh-host-proxy` is a small TCP proxy for use with OpenSSH `ProxyCommand`.

It is meant for the case where the same machine can be reached through multiple routes, and the best route depends on where your client machine currently is, for example your laptop:

- the host may be reachable over an ad-hoc direct high-speed Ethernet link, for example a USB 10GbE adapter with static IPs on both ends
- when you are on the local network, the host may also be reachable over normal wired LAN or local Wi-Fi
- when you are outside home, the same host may only be reachable through Tailscale or another VPN path

The main reason this exists is that although Tailscale can often switch to a direct path and get close to normal LAN performance, that is not always the best route available in a home setup. In my case I also use a static IP on a USB 10GbE interface for large transfers. When that link is connected, it is substantially faster than the normal local LAN or Wi-Fi path, so I want everything that uses SSH under the hood, such as `rsync` and `sftp`, to switch to that better route automatically without any extra reconfiguration. The routing change is entirely seamless: connect the USB network adapter, and the preferred path changes by itself.

The point is to keep your SSH usage simple. You still run:

```
ssh user@host
```

and let `ProxyCommand` in `~/.ssh/config` choose the best reachable target automatically.

## How It Works

You pass targets in priority order:

```
ssh-host-proxy --targets 192.168.1.10:22,host.local:22,100.64.10.20:22
```

The proxy then:

1. starts one TCP connection attempt per target, all in parallel
2. keeps those connection attempts in flight for up to `--connect-timeout` total
3. every `--selection-interval`, checks whether any target has already responded
4. if multiple targets have responded, picks the first one in the order you provided
5. picks immediately if a target succeeds and all higher-priority targets have already failed; it also finishes early if all probe attempts complete
6. once a target is picked, the remaining in-flight probe attempts are canceled
7. in normal mode, it reuses that already-connected socket for the SSH session instead of dialing again
8. exits with error if no target responds

In normal mode it proxies stdin/stdout to the selected host, which makes it suitable for OpenSSH `ProxyCommand`.

## Runtime Modes

There are two ways to use `ssh-host-proxy` with OpenSSH:

1. normal proxy mode
2. file descriptor passing mode with `--fdpass`

Normal proxy mode is the compatibility path. It still reuses the already-connected winning socket, but `ssh-host-proxy` remains in the data path for the lifetime of the SSH session and relays bytes between `ssh` and the remote socket.

`--fdpass` should be preferred when your OpenSSH client supports `ProxyUseFdpass`. In that mode, `ssh-host-proxy` is only involved during connection establishment: it selects the best route, opens the winning connection, passes that connected socket to `ssh`, and exits. After that, the transfer path is native. There is no extra proxy process relaying traffic at runtime, so data transfer performance is effectively the same as if no proxy helper had been used at all.

In practice that means the route-selection step may add a small amount of latency while the best path is chosen, but once the SSH connection is established, transfers run at native performance.

Use the non-`fdpass` mode only when `ProxyUseFdpass` is not available, for example because the OpenSSH client is too old or the local platform does not support the required descriptor passing behavior.

## Example SSH Config

`~/.ssh/config`

```
Host EXAMPLE
    ProxyCommand ssh-host-proxy --targets 192.168.1.10:22,mybox.local:22,100.64.10.20:22
```

After that, from your client machine you just use:

```
ssh user@EXAMPLE
```

If your OpenSSH supports it, you can avoid keeping `ssh-host-proxy` in the transfer path entirely by using file descriptor passing:

`~/.ssh/config`

```
Host EXAMPLE
    ProxyCommand ssh-host-proxy --fdpass --targets 192.168.1.10:22,mybox.local:22,100.64.10.20:22
    ProxyUseFdpass yes
```

With that configuration, `ssh-host-proxy` selects the route, passes the connected socket to `ssh`, and exits.

## Options

```
Usage:
  ssh-host-proxy --targets host1:22,host2:22,host3:22 [--selection-interval 1s] [--connect-timeout 10s] [--fdpass] [--dry-run]

Options:
  --targets string
        Comma-separated host:port targets in priority order
  --selection-interval duration
        How often to evaluate completed probes for target selection (default 1s)
  --connect-timeout duration
        Maximum total time to wait for a reachable target (default 10s)
  --fdpass
        Pass the connected socket to ssh instead of proxying traffic in-process
  --dry-run
        Print what would be selected and exit
  --help
        Print help and exit
```

`--dry-run` takes precedence over `--fdpass`: it reports the selection and exits
without passing a descriptor or relaying traffic. It reports only probe results
available when selection finishes, so pending targets may be omitted. Help,
diagnostics, and dry-run output go to stderr.

Exit codes:

- `0`: successful operation, including help or usage displayed with no arguments
- `1`: runtime failure during selection, descriptor passing, or proxying
- `2`: invalid command-line arguments

If all connection attempts fail before the deadline, the diagnostic is
`all target connection attempts failed`. If the overall deadline expires with
no reachable target, it is
`no reachable targets responded before --connect-timeout`.

## Build

Local platform build:

```
make build
```

This produces `build/<goos>-<goarch>/bin/ssh-host-proxy`.

Local CGO-disabled build:

```
make static
```

This produces `build/<goos>-<goarch>/bin/ssh-host-proxy-static`. The `static`
target name is retained for the release workflow: Linux outputs are statically
linked, while the Darwin output still links standard macOS system libraries.

Release builds:

```
make release
```

This produces stripped, CGO-disabled release binaries in platform build
directories:

- `build/darwin-arm64/bin/ssh-host-proxy-static`
- `build/linux-arm64/bin/ssh-host-proxy-static`
- `build/linux-amd64/bin/ssh-host-proxy-static`

Release asset staging:

```
make release-assets
```

This copies the release binaries to `build/release-assets/` with unique
platform-suffixed filenames for GitHub Release uploads.

## Development Checks

Run tests and static analysis with repository-local Go state:

```
make test
make test-race
make vet
```

`make test-race` requires a host platform supported by Go's race detector,
CGO enabled, and a suitable C compiler. Ordinary `make test` remains available
without the race detector.

Compile tests for another platform without running them:

```
make test-compile GOOS=windows GOARCH=amd64
```

This writes `build/windows-amd64/bin/ssh-host-proxy.test.exe`. Other targets use
`build/<goos>-<goarch>/bin/ssh-host-proxy.test` (with `.exe` on Windows).
CI uses the Windows compile check to cover the non-Unix fdpass fallback;
cross-compilation does not verify runtime behavior on that platform.

## Install

```
make install
```

Default install behavior:

- when run as root, copies the local platform binary to `/usr/local/bin/ssh-host-proxy`
- when run as a normal user, copies the local platform binary to `~/.local/bin/ssh-host-proxy`
