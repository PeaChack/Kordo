# Local copy of github.com/tailscale/wf

Version: v0.0.0-20240214030419-6fbb0a674ee6 (BSD-3-Clause, see LICENSE).

Patch: `malloc.go` — the arena allocator rounds every allocation up to pointer
alignment. Upstream bump-allocates byte blobs back to back, so a following
struct with pointer fields could be misaligned; Go 1.26's write barrier then
aborts the process ("bulkBarrierPreWrite: unaligned arguments"). This crashed
the Portico service whenever kill-switch filters with app IDs were installed.

Wired in via `replace github.com/tailscale/wf => ./third_party/wf` in go.mod.
