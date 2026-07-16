# pg-test — duplicate `IS NULL` selectivity demo

A small Go program that demonstrates how PostgreSQL 18's planner estimates row
counts when a query repeats an identical predicate. It:

1. Starts a **PostgreSQL 18** server in Docker (`postgres:18`), and tears it down on exit.
2. Loads **exactly 100,000 rows** into `records(id TEXT, deleted_at TIMESTAMPTZ)` —
   `id` is a ULID and **exactly 50,000** rows have `deleted_at = NULL`.
3. Runs `ANALYZE`, then runs **5 queries** under `EXPLAIN (ANALYZE, FORMAT JSON)`.
   Query *N* carries *N−1* copies of `deleted_at IS NULL`
   (Q1 has none, Q5 has four).
4. Prints the plan tree per query plus a summary of the **estimated** rows.

## Results

Postgres does **not** deduplicate identical predicates — each repeated
`deleted_at IS NULL` is treated as independent, so the estimate roughly halves
each time, even though all five queries match the same 50,000 rows.

Measured on a sample run against PostgreSQL 18.4 (see `results.log`):

| Query | `IS NULL` clauses | Estimated rows (scan node) | Actual matched* | Ratio vs. prev. |
|------:|------------------:|---------------------------:|----------------:|----------------:|
| 1 | 0 | 100000 | 100 | — |
| 2 | 1 | 49847 | 100 | 0.50× |
| 3 | 2 | 24847 | 100 | 0.50× |
| 4 | 3 | 12385 | 100 | 0.50× |
| 5 | 4 | 6174 | 100 | 0.50× |

\* `Actual matched` is capped at 100 because `LIMIT 100` stops the scan early; the
true match count is 50,000 for every query.

<details>
<summary>Full run output (<code>results.log</code>)</summary>

For each query the program prints the SQL, the exact `EXPLAIN` statement sent
(with `--raw`), the raw JSON plan, and the summarized plan tree. The
distinguishing lines from each query's `Seq Scan` node:

```
Query 1 — WHERE (none)
  Filter:    (none)
  Plan Rows: 100000

Query 2 — WHERE deleted_at IS NULL
  Filter:    (deleted_at IS NULL)
  Plan Rows: 49847

Query 3 — WHERE deleted_at IS NULL AND deleted_at IS NULL
  Filter:    ((deleted_at IS NULL) AND (deleted_at IS NULL))
  Plan Rows: 24847

Query 4 — WHERE deleted_at IS NULL AND deleted_at IS NULL AND deleted_at IS NULL
  Filter:    ((deleted_at IS NULL) AND (deleted_at IS NULL) AND (deleted_at IS NULL))
  Plan Rows: 12385

Query 5 — WHERE deleted_at IS NULL AND deleted_at IS NULL AND deleted_at IS NULL AND deleted_at IS NULL
  Filter:    ((deleted_at IS NULL) AND (deleted_at IS NULL) AND (deleted_at IS NULL) AND (deleted_at IS NULL))
  Plan Rows: 6174
```

Program summary table:

```
Query    IS NULL clauses  Estimated rows   Actual matched
1        0                100000           100
2        1                49847            100
3        2                24847            100
4        3                12385            100
5        4                6174             100
```

</details>

## Prerequisites

- A running **Docker** daemon (Docker Desktop, colima, OrbStack, …).
- Either [Nix](https://nixos.org) with flakes, **or** Go 1.26.4.

## Run it

With Nix (no local Go needed):

```sh
nix run
```

Or drop into a dev shell (pins Go 1.26.4, adds `gopls` and the `psql` client):

```sh
nix develop
go run .
```

Without Nix:

```sh
go run .
```

## Flags

- `--raw` — for each query, also print the exact `EXPLAIN` statement sent and the
  full raw JSON response before the summarized plan tree:

  ```sh
  go run . --raw      # or: nix run . -- --raw
  ```

## Notes

- The per-query `actual.rows=100` reflects the `LIMIT 100` stopping the scan
  early — **not** the true match count (which is 50,000 for every query). The
  interesting number is the **scan-node estimate**, which is what the summary
  table reports.
- The container listens on host port `55432`; change `hostPort` in `main.go` if
  that clashes.
