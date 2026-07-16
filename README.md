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

## The result

Postgres does **not** deduplicate identical predicates — each repeated
`deleted_at IS NULL` is treated as independent, so the estimate roughly halves
each time, even though all five queries match the same 50,000 rows:

| Query | `IS NULL` clauses | Estimated rows |
|------:|------------------:|---------------:|
| 1 | 0 | 100000 |
| 2 | 1 | ~50000 |
| 3 | 2 | ~25000 |
| 4 | 3 | ~12500 |
| 5 | 4 | ~6250 |

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

## Notes

- The per-query `actual.rows=100` reflects the `LIMIT 100` stopping the scan
  early — **not** the true match count (which is 50,000 for every query). The
  interesting number is the **scan-node estimate**, which is what the summary
  table reports.
- The container listens on host port `55432`; change `hostPort` in `main.go` if
  that clashes.
