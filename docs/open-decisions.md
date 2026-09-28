# Open decisions

What is built and waiting, and what needs a call before it can be built. The
[roadmap](roadmap.md) lists longer-term work; this page lists what is blocking now.

## Agent rollout

Self-update is per host through `KLOUDVIEW_AUTO_UPDATE`, which lets the server place
code on a machine. The installer turns it on, because a host that ignores the build it
is offered leaves a rollout reporting success while the fleet stays where it was;
`--no-auto-update` pins one instead. Naming `KLOUDVIEW_AGENT_TARGET_VERSION` is then
enough to move the fleet, so `KLOUDVIEW_AGENT_CANARY` stages it behind one node: that
node takes a build first and the rest are offered the version they already run until it
has held the new one for the soak period. Every canary failure — silent, stuck, or a
name matching no enrolled agent — holds the fleet. Once cleared, agents take the build
across a window rather than at once.

**Open:** the canary is a single named node. Percentage waves and automatic rollback on
a failed soak are not built; recovery from a bad build is manual, using the `.previous`
binary each agent keeps beside its own.

## Process lifecycle capture

Polling `/proc` misses anything short-lived, which is often what caused an incident.
Catching every exec and exit needs kernel eventing, and each option has a different
privilege cost:

| Approach | Agent privilege | argv | Short-lived |
|---|---|---|---|
| `/proc` polling (today) | none | sampled only | missed |
| auditd `execve` rules, read through the log stream | none | complete | caught |
| netlink proc connector | `CAP_NET_ADMIN` | racy | mostly |
| eBPF tracepoints | `CAP_BPF` + `CAP_PERFMON` | complete | complete |

`CAP_PERFMON` allows reading other processes' memory through kernel tracing, which is
close to root. Combined with self-update it means a compromised server reaches the
kernel on every node. eBPF without BTF is possible — the `sched_process_exec` and
`sched_process_exit` tracepoints have a stable ABI — but it needs clang in the build
and a new dependency, and BPF loading cannot be exercised in this repository's test
environment.

**Recommendation:** auditd. The privileged component is then a distro-maintained
daemon rather than our code, and the agent stays unprivileged. Its `execve` records
are log lines, so the continuous log stream already carries them — no new collector
and no new agent privilege. What remains is a rule-scope decision: `execve` auditing
is off by default and, turned on wholesale, produces hundreds to thousands of records
a second on a busy host, so it has to start narrow (uid ≥ 1000, failed execs, setuid
binaries). If eBPF is preferred instead, put it in a separate small helper that holds
the capabilities, so self-update cannot replace the privileged part.

## Blind spots when a node dies

A host that loses power cannot be asked anything afterwards. Two pieces close most of
the gap; neither is built:

1. **Agent spool.** Failed metric sends are dropped, so a ten-minute network cut is a
   permanent hole in the metric series even after the agent returns. A bounded on-disk
   queue flushed on reconnect turns that into a delay. Log lines already survive this:
   the journal cursor only advances once the server has accepted a window.
2. **journald persistence.** Distributions default to `Storage=auto`, which keeps the
   journal in tmpfs when `/var/log/journal` is absent — a reboot then erases the
   reason for the reboot. The installer should create the directory and set a size.

Already shipped: a disconnection is recorded as an alert with the last metric sample
and whether other nodes fell silent at the same time, and warning-and-worse lines
are streamed continuously so a node-side failure has left evidence on the server
before the node goes quiet.

Not solvable in software: the moment of a kernel panic or a power cut. That needs
IPMI/iDRAC SEL or netconsole.

## What "normal" means

The agent reports log volume by severity every minute, so "normal" is measurable.
The counters are held in memory for 24 hours and are not persisted or exposed as an
alert-rule metric, which is what would let a crash loop or a brute-force burst raise
an alert on its own.

**Open:** streamed log lines and their counters are the only state that does not
survive a server restart. The stream exists to move evidence off a node before it goes
quiet, not to be an archive, and a window worth keeping is captured on demand instead —
but a restart during an incident loses the window that incident produced.

## Collection depth

- **Containers** report CPU, memory, block IO, and process count from the cgroup
  filesystem, so they carry a trend and can be alerted on like a node.
- **VMs** report the hypervisor's view. Guest filesystem usage and guest processes
  are invisible from the host at any depth; monitoring inside a guest needs an agent
  inside it. Using libvirt through a library rather than the `virsh` CLI is on hold.
- **Environment variables** need a server-to-agent configuration channel so a node's
  allowed keys can be set from the console. The agent side is done; the channel is
  not.

## Retention at scale

Measured: four nodes reporting every ten seconds produce 9 MB of raw samples a day.

| Nodes | Raw 90 days | One-minute rollups 400 days | Total |
|---|---|---|---|
| 4 | 0.8 GB | 0.5 GB | 1.3 GB |
| 50 | 9.9 GB | 5.9 GB | 15.8 GB |

Defaults are raw 30 days and rollups 400 days, which is about 9 GB at fifty nodes and
keeps a month at full resolution. Raising `KLOUDVIEW_METRIC_RAW_DAYS` to 90 costs
roughly 16 GB there. Beyond that, monthly partitions make pruning cheap; deleting rows
daily does not scale as well.

Logs are a different shape: journald on a busy host is tens to hundreds of megabytes a
day, so the same retention is not affordable and short windows plus severity filtering
are the realistic approach.

