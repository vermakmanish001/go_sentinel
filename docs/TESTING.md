# Testing GoSentinel

How to confirm the system works end to end: CLI → orchestrator → workers → live
HTTP load → metrics aggregation → dashboard.

Checks are layered. Tier 0 needs only Go. Tiers 1–6 need Docker, because the
production orchestrator hard-requires etcd — see [Why Docker is mandatory](#why-docker-is-mandatory).

> **Status:** last executed end to end against a 3-worker stack — all tiers
> pass. Reference numbers throughout are from that run.

---

## Prerequisites

| Tool | Why | Check |
|---|---|---|
| Go 1.23+ | build + unit tests | `go version` |
| `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc` | `make proto` / `make build` | `protoc --version` |
| Docker daemon | etcd, workers, load target | `docker info` |
| `docker-compose` (v1, hyphenated) | the Makefile targets call this binary | `which docker-compose` |

Run everything from the repository root — the CLI resolves config from
`./configs/config.yaml`.

---

## Tier 0 — Offline checks (no Docker)

```bash
make build          # regenerates protobufs, builds all three binaries
go vet ./...
go test ./... -race
```

`make build` must leave `proto/*/*.pb.go` byte-identical. If `git status` shows
generated files modified, your protoc plugin versions differ from those used to
commit them.

There is **no `validate` or dry-run subcommand** — a test plan is only parsed
when you `run` it. To check a plan without generating load, point it at a
throwaway target and use a 1-second single stage.

---

## Why Docker is mandatory

`NodeManager.RegisterNode` (`internal/orchestrator/node_manager.go`) writes each
worker to etcd **while holding `nm.mu`**, and that `Put` carries no timeout. With
etcd absent the call blocks forever and the first registration deadlocks the
whole node registry. The orchestrator does not degrade gracefully — it hangs.

`GOSENTINEL_DEV_MODE=true ./bin/orchestrator` skips etcd, but its in-memory stub
**never dispatches work to workers** and streams empty snapshots. It exercises
registration and CLI plumbing only. Never read a green dev-mode run as
end-to-end verification.

---

## Tier 1 — Bring up the stack

```bash
open -a Docker                  # macOS
docker info >/dev/null && echo "daemon up"

make up
docker-compose -f docker/docker-compose.yml ps
```

Eight services: `etcd`, `orchestrator`, 3× `worker`, `httpbin` (load target),
`jaeger`, `prometheus`, `grafana`.

Confirm Prometheus is scraping all four Go processes:

```bash
curl -s 'http://localhost:9091/api/v1/targets?state=active' | python3 -c "
import sys,json
for t in json.load(sys.stdin)['data']['activeTargets']:
    print(f\"{t['labels'].get('job'):14s} {t['scrapeUrl']:40s} {t['health']}\")"
```

Expect `orchestrator` plus `docker-worker-1..3` all `up`.

---

## Tier 2 — Confirm the fleet registered

```bash
./bin/cli nodes
```

Expect three rows, addresses `<container-id>:50052`, Max VUs 1000 each, recent
Last Seen, and status `idle`. Status tracks the `test_active` flag on incoming
metric batches, so it reads `running` only while a test really is in flight.

If the list is empty:

```bash
docker-compose -f docker/docker-compose.yml logs orchestrator | grep -i "node registered"
docker-compose -f docker/docker-compose.yml logs worker       | grep -i register
```

Workers retry registration three times, two seconds apart, then **give up
permanently**. A worker that started before the orchestrator was listening will
never retry — restart it.

---

## Tier 3 — Run a load test

```bash
./bin/cli run examples/basic_load_test.yaml
```

Runs 2 minutes: 5 → 10 → 5 VUs over 30s / 1m / 30s. The plan targets
`http://httpbin:8080`, which resolves only inside the Docker network — correct,
since the **workers** issue the requests and they live there.

### Driving it headlessly

`cli run` launches a Bubbletea TUI and fails with
`could not open a new TTY` in a non-interactive shell. Dispatch happens *before*
the TUI starts, so the test still runs on the workers. For scripted verification,
dispatch this way and observe through the orchestrator's metrics stream or the
worker gauges instead of the dashboard.

---

## Tier 4 — Verify metrics correctness

### 4a. Load curve reproduced across the fleet

The orchestrator scales each stage to each worker before dispatch. Confirm the
per-worker targets sum to the plan's stage target:

```bash
docker-compose -f docker/docker-compose.yml logs worker | grep "starting stage"
```

Reference values from a passing run (plan 5 → 10 → 5, three equal workers).
Throughput must scale with the VU count, not just the allocation:

| Stage | w1 | w2 | w3 | Sum | Plan target | Measured RPS |
|---|---|---|---|---|---|---|
| 0 | 1 | 2 | 2 | **5** | 5 | 94.8 |
| 1 | 3 | 3 | 4 | **10** | 10 | **190.1** (2.00×) |
| 2 | 1 | 2 | 2 | **5** | 5 | 96.1 |

If the allocation is right but RPS stays flat across stages, the virtual users
are not actually running — check that they are being reused across stages
rather than dying after the first `Stop`.

If every worker reports the same number in every stage and the sum sits flat at
the peak, per-worker stage scaling is broken.

To watch actual live VUs at finer resolution than Prometheus's 15s scrape, poll
the worker gauges directly:

```bash
for w in docker-worker-1 docker-worker-2 docker-worker-3; do
  docker exec $w wget -qO- http://localhost:9091/metrics | awk '/^gosentinel_active_vus /{print "'$w'", $2}'
done
```

### 4b. Aggregate metrics must not drop out

Workers report every second, plus a 10-second keep-alive. Both must send a
**complete** batch — the aggregator replaces a worker's entry wholesale, so a
partial batch zeroes that worker's contribution until its next full report.

Watch RPS across several keep-alive ticks; it must never read 0 mid-test.
A passing run: **86 consecutive 1-second samples under load, zero readings of
0.00**.

Also confirm the cadence is per-second and stays that way for **repeat** runs on
the same workers — the reporter is restarted for each test, and a regression
there silently drops later runs to the 10s keep-alive resolution. A passing
second run showed **120 distinct request-count values across 134 samples**.

### 4c. Error rate must be dimensionally sane

`cli status` prints only error *percentage*. To see errors **per second**, read
the metrics stream directly (`StreamMetrics`). Point a plan at an always-failing
endpoint:

```bash
cat > /tmp/error_test.yaml <<'YAML'
name: Error Rate Check
stages:
  - duration: 30s
    target_vus: 9
http:
  base_url: "http://httpbin:8080"
  timeout: 5s
  requests:
    - method: GET
      path: /status/500
      assertions:
        - status_code: 200
YAML

./bin/cli run /tmp/error_test.yaml
```

With every request failing, **errors/sec must equal average RPS** and the
percentage must read 100%. A passing run measured
`err_per_s / rps_avg = 1.0000` across 39 steady-state samples.

An error rate many times larger than RPS, growing over the run, means the rate is
being computed from a cumulative counter divided by the aggregation interval
instead of summed across workers.

Metrics are keyed by test id, so a new run starts from zero rather than
inheriting the previous run's totals.

---

## Tier 5 — Observability surfaces

| Surface | URL | Expected |
|---|---|---|
| Orchestrator metrics | http://localhost:9090/metrics | Go runtime metrics only |
| Prometheus | http://localhost:9091 | Targets: orchestrator + `docker-worker-1..3` UP |
| Grafana | http://localhost:3000 (admin/admin) | Loads, but **no dashboards** |
| Jaeger | http://localhost:16686 | **No traces** |
| httpbin | http://localhost:8080 | The load target |

---

## Tier 6 — Lifecycle and teardown

```bash
./bin/cli status <test-id>     # RUNNING -> COMPLETED once every worker finishes
./bin/cli stop   <test-id>     # aborts the run on every assigned worker
make down
```

A completed run reports `Status: COMPLETED`: workers signal completion via the
`test_active` flag, and the orchestrator marks the run done once every assigned
worker has reported it.

`stop` fans out the `StopTest` RPC to each assigned worker and reports how many
accepted. A passing run went from 5 active VUs to **0 within 4 seconds**, with
status `STOPPED`. Note that `Current RPS` stays non-zero briefly afterwards —
that is the trailing 10-second averaging window draining, not load still
running; confirm with the VU gauges above.

---

## Expected noise — looks broken, isn't

1. **`docker-compose ps` reports orchestrator and workers `unhealthy`.** Both
   healthchecks invoke `grpc_health_probe`, which is not installed in either
   image (`docker/*.Dockerfile` install only `ca-certificates` and `tzdata`). The
   gRPC health service is registered and serving. Nothing gates on the
   healthcheck — `worker.depends_on.orchestrator` has no `condition` — so the
   stack is fully functional.
2. **Worker addresses are not reachable from your host.** Workers register their
   container hostname, dialable by the orchestrator inside the Docker network.
3. **`git status` shows `bin/*` modified after every build.** The binaries are
   committed to the repository.
4. **`examples/spike_test.yaml` fails 100%** — it targets `target-service:8080`,
   not a service in the compose file.
5. **`make scale` to 5 workers leaves two unscraped** — `configs/prometheus.yml`
   hardcodes exactly three worker targets.

---

## Known gaps — genuinely not implemented

**1. Jaeger shows no traces.** `tracer.Setup` builds and installs a provider, but
no span is ever started anywhere in the codebase. Tracing is scaffolding.

**2. Grafana has no dashboards.** None are provisioned in the compose file.

**3. Completed runs are retained in memory.** The orchestrator keeps finished
tests and their metrics so they stay queryable, and never evicts them. Fine for
interactive use; a long-lived orchestrator will grow until restarted.

**4. Aggregate percentiles are averaged across workers,** not merged. The
correct approach is merging the HDR histograms, but the raw histograms are not
shipped over the wire. Fleet p95/p99 are therefore approximate; per-worker
values are exact.

---

## Troubleshooting

**`no worker nodes available`** — nothing registered. See Tier 2.

**`insufficient worker capacity: need N, have M`** — the plan's peak `target_vus`
exceeds combined `max_vus` (1000/worker default). Lower the peak or `make scale`.

**CLI cannot reach the orchestrator** — the CLI dials `localhost:50051`; confirm
the port is published with
`docker-compose -f docker/docker-compose.yml ps orchestrator`.

**Metrics frozen at old values** — the orchestrator never expires worker entries.
Stale numbers persist until that worker reports again.
