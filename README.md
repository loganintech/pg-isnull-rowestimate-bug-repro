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

Measured on a sample run against PostgreSQL 18.4 (see `results.log`). The
queries run without a `LIMIT`, so `Actual matched` is the true count of matching
rows — identical (50,000) for every filtered query, while the **estimate** keeps
halving. `Underestimate scale` is `Actual matched ÷ Estimated rows`, shown only
where the planner actually underestimates: Query 1 has no filter and Query 2's
estimate is essentially exact (50,017 vs. 50,000 — sampling noise, and slightly
*over*), so the compounding error only begins with the first duplicate clause
in Query 3:

| Query | `IS NULL` clauses | Estimated rows (scan node) | Actual matched | Underestimate scale | Ratio vs. prev. |
|------:|------------------:|---------------------------:|---------------:|--------------------:|----------------:|
| 1 | 0 | 100000 | 100000 | — (exact) | — |
| 2 | 1 | 50017 | 50000 | — (accurate) | 0.50× |
| 3 | 2 | 25017 | 50000 | 2.0× | 0.50× |
| 4 | 3 | 12513 | 50000 | 4.0× | 0.50× |
| 5 | 4 | 6258 | 50000 | 8.0× | 0.50× |

By Query 5, repeating a predicate that changes nothing makes the planner
underestimate the result by ~8×. (Exact estimates shift slightly between runs
since `ANALYZE` samples the table.)

<details>
<summary>Full run output (<code>results.log</code>)</summary>

For each query the program prints the SQL, the exact `EXPLAIN` statement sent
(with `--raw`), the raw JSON plan, and the summarized plan tree. The
distinguishing lines from each query's `Seq Scan` node:

```
Query 1 — WHERE (none)
  Filter:    (none)
  Plan Rows: 100000   Actual Rows: 100000

Query 2 — WHERE deleted_at IS NULL
  Filter:    (deleted_at IS NULL)
  Plan Rows: 50017    Actual Rows: 50000

Query 3 — WHERE deleted_at IS NULL AND deleted_at IS NULL
  Filter:    ((deleted_at IS NULL) AND (deleted_at IS NULL))
  Plan Rows: 25017    Actual Rows: 50000

Query 4 — WHERE deleted_at IS NULL AND deleted_at IS NULL AND deleted_at IS NULL
  Filter:    ((deleted_at IS NULL) AND (deleted_at IS NULL) AND (deleted_at IS NULL))
  Plan Rows: 12513    Actual Rows: 50000

Query 5 — WHERE deleted_at IS NULL AND deleted_at IS NULL AND deleted_at IS NULL AND deleted_at IS NULL
  Filter:    ((deleted_at IS NULL) AND (deleted_at IS NULL) AND (deleted_at IS NULL) AND (deleted_at IS NULL))
  Plan Rows: 6258     Actual Rows: 50000
```

Program summary table:

```
Query    IS NULL clauses  Estimated rows   Actual matched   Underestimate scale
1        0                100000           100000           - (accurate)
2        1                50017            50000            - (accurate)
3        2                25017            50000            2.0x
4        3                12513            50000            4.0x
5        4                6258             50000            8.0x
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

- The queries run without a `LIMIT`, so each `Seq Scan` reports both the
  planner's **estimate** (`Plan Rows`) and the **true** number of matching rows
  (`Actual Rows`). The summary table reports the scan-node estimate against that
  actual.
- The container listens on host port `55432`; change `hostPort` in `main.go` if
  that clashes.
