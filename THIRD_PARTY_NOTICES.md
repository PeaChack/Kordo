# Third-party notices

Portico is licensed under **GPL-3.0-or-later** (see `LICENSE`). It embeds or
links the following components.

## sing-box — GPL-3.0-or-later (with additional terms)

Network engine, embedded as a Go library (version pinned in `go.mod`, 1.14.4).
Source: https://github.com/SagerNet/sing-box

The sing-box license adds a condition under GPL section 7: derivative works may
not use the name "sing-box" or imply association with the project. Portico is
an independent project and is not affiliated with or endorsed by sing-box or
SagerNet.

## Wintun — Prebuilt Binaries License

Portico uses the unmodified, signed `wintun.dll` from https://www.wintun.net,
embedded by the `sing-tun` library and loaded from memory. Wintun is
distributed under the Wintun Prebuilt Binaries License, which permits
redistribution of the unmodified binary only. Wintun source code is
GPL-2.0 (https://git.zx2c4.com/wintun).

## tailscale/wf — BSD-3-Clause

Windows Filtering Platform bindings used by the kill switch.
https://github.com/tailscale/wf — Copyright (c) Tailscale Inc & AUTHORS.

## Wails — MIT

Desktop GUI framework. https://github.com/wailsapp/wails — Copyright (c) Lea Anthony.

## go-winio — MIT

Named pipes. https://github.com/microsoft/go-winio — Copyright (c) Microsoft.

## miekg/dns — BSD-3-Clause

DNS client used to resolve server addresses. https://github.com/miekg/dns

## Go standard library and golang.org/x — BSD-3-Clause

Copyright (c) The Go Authors.

The full dependency list with versions is in `go.mod` / `go.sum`; each module's
license is shipped in its source distribution.
