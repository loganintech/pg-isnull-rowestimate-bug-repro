// Command pg-test spins up a PostgreSQL 18 server in Docker, loads exactly
// 100,000 rows (half with a NULL deleted_at), then runs five queries under
// EXPLAIN ANALYZE. Each successive query appends another duplicate
// `deleted_at IS NULL` predicate. The point is to observe how the planner's
// *estimated* row count changes as identical predicates are repeated.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

const (
	containerName = "pg-test-18"
	image         = "postgres:18"
	hostPort      = "55432"
	pgPassword    = "postgres"
	rowCount      = 100_000
)

func main() {
	rawOutput := flag.Bool("raw", false, "print the raw EXPLAIN statement sent and the raw JSON returned for each query")
	flag.Parse()

	if err := run(*rawOutput); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(rawOutput bool) error {
	ctx := context.Background()

	if err := startPostgres(ctx); err != nil {
		return fmt.Errorf("start postgres: %w", err)
	}
	defer stopPostgres()

	dsn := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/postgres?sslmode=disable", pgPassword, hostPort)

	conn, err := connectWithRetry(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if v, err := serverVersion(ctx, conn); err == nil {
		log.Printf("connected to: %s", v)
	}

	if err := loadData(ctx, conn); err != nil {
		return fmt.Errorf("load data: %w", err)
	}

	if err := runExplainQueries(ctx, conn, rawOutput); err != nil {
		return fmt.Errorf("explain queries: %w", err)
	}

	return nil
}

// startPostgres removes any stale container then starts a fresh postgres:18.
func startPostgres(ctx context.Context) error {
	_ = exec.Command("docker", "rm", "-f", containerName).Run()

	log.Printf("starting %s container %q on host port %s ...", image, containerName, hostPort)
	out, err := exec.Command("docker", "run", "-d",
		"--name", containerName,
		"-e", "POSTGRES_PASSWORD="+pgPassword,
		"-p", hostPort+":5432",
		image,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run: %v: %s", err, out)
	}
	return nil
}

func stopPostgres() {
	log.Printf("removing container %q ...", containerName)
	_ = exec.Command("docker", "rm", "-f", containerName).Run()
}

// connectWithRetry waits for the server to accept connections.
func connectWithRetry(ctx context.Context, dsn string) (*pgx.Conn, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			if err = conn.Ping(ctx); err == nil {
				return conn, nil
			}
			_ = conn.Close(ctx)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for postgres: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func serverVersion(ctx context.Context, conn *pgx.Conn) (string, error) {
	var v string
	err := conn.QueryRow(ctx, "SHOW server_version").Scan(&v)
	return "PostgreSQL " + v, err
}

// loadData creates the table and bulk-loads exactly rowCount rows, exactly half
// of which have a NULL deleted_at.
func loadData(ctx context.Context, conn *pgx.Conn) error {
	log.Printf("creating table and loading %d rows ...", rowCount)

	if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS records`); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE records (
			id         TEXT        NOT NULL,
			deleted_at TIMESTAMPTZ
		)`); err != nil {
		return err
	}

	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	rows := make([][]any, rowCount)
	nullCount := 0
	for i := range rowCount {
		id := ulid.Make().String()
		var deletedAt *time.Time
		if i%2 != 0 {
			t := base.Add(time.Duration(i) * time.Second)
			deletedAt = &t
		} else {
			nullCount++
		}
		rows[i] = []any{id, deletedAt}
	}

	n, err := conn.CopyFrom(ctx, pgx.Identifier{"records"}, []string{"id", "deleted_at"}, pgx.CopyFromRows(rows))
	if err != nil {
		return err
	}
	log.Printf("inserted %d rows (%d with NULL deleted_at)", n, nullCount)

	// ANALYZE so the planner has accurate statistics for its estimates.
	if _, err := conn.Exec(ctx, `ANALYZE records`); err != nil {
		return err
	}
	return nil
}

// --- EXPLAIN plan parsing -------------------------------------------------

type planNode struct {
	NodeType   string     `json:"Node Type"`
	PlanRows   float64    `json:"Plan Rows"`
	ActualRows float64    `json:"Actual Rows"`
	Plans      []planNode `json:"Plans"`
}

type explainRoot struct {
	Plan planNode `json:"Plan"`
}

// firstScan returns the deepest/first scan node, whose Plan Rows reflects the
// full selectivity estimate for the WHERE clause and whose Actual Rows is the
// true number of matching rows.
func firstScan(n planNode) *planNode {
	if len(n.NodeType) >= 4 && n.NodeType[len(n.NodeType)-4:] == "Scan" {
		return &n
	}
	for _, c := range n.Plans {
		if s := firstScan(c); s != nil {
			return s
		}
	}
	return nil
}

// indentJSON pretty-prints raw JSON, falling back to the original bytes if it
// cannot be parsed.
func indentJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func printTree(n planNode, depth int) {
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	fmt.Printf("%s-> %-14s est.rows=%-8.0f actual.rows=%.0f\n", indent, n.NodeType, n.PlanRows, n.ActualRows)
	for _, c := range n.Plans {
		printTree(c, depth+1)
	}
}

// runExplainQueries builds the five queries and runs each under EXPLAIN ANALYZE.
// When rawOutput is true, the exact statement sent and the raw JSON returned are
// printed for each query.
func runExplainQueries(ctx context.Context, conn *pgx.Conn, rawOutput bool) error {
	fmt.Println()
	fmt.Println("=========================================================================")
	fmt.Println(" EXPLAIN ANALYZE — estimated rows vs. number of duplicate IS NULL clauses")
	fmt.Println("=========================================================================")

	type summary struct {
		q          int
		clauses    int
		sql        string
		scanEst    float64
		scanActual float64
	}
	var summaries []summary

	for q := 1; q <= 5; q++ {
		clauses := q - 1 // Q1=0, Q2=1, ... Q5=4
		where := ""
		for c := 0; c < clauses; c++ {
			if c == 0 {
				where = " WHERE deleted_at IS NULL"
			} else {
				where += " AND deleted_at IS NULL"
			}
		}
		query := fmt.Sprintf("SELECT id, deleted_at FROM records%s", where)
		statement := "EXPLAIN (ANALYZE, FORMAT JSON) " + query

		var raw []byte
		err := conn.QueryRow(ctx, statement).Scan(&raw)
		if err != nil {
			return fmt.Errorf("query %d: %w", q, err)
		}

		var roots []explainRoot
		if err := json.Unmarshal(raw, &roots); err != nil {
			return fmt.Errorf("parse plan %d: %w", q, err)
		}

		fmt.Printf("\n--- Query %d — %d duplicate `deleted_at IS NULL` clause(s) ---\n", q, clauses)
		fmt.Printf("SQL: %s\n", query)
		if rawOutput {
			fmt.Printf("RAW statement sent:\n  %s\n", statement)
			fmt.Printf("RAW EXPLAIN response:\n%s\n", indentJSON(raw))
		}
		printTree(roots[0].Plan, 0)

		s := summary{q: q, clauses: clauses, sql: query}
		if scan := firstScan(roots[0].Plan); scan != nil {
			s.scanEst = scan.PlanRows
			s.scanActual = scan.ActualRows
		}
		summaries = append(summaries, s)
	}

	fmt.Println()
	fmt.Println("=========================================================================")
	fmt.Println(" SUMMARY — scan-node estimated rows (the filter selectivity estimate)")
	fmt.Println("=========================================================================")
	fmt.Printf("%-8s %-16s %-16s %-16s %-20s\n", "Query", "IS NULL clauses", "Estimated rows", "Actual matched", "Underestimate scale")
	for _, s := range summaries {
		underestimate := "n/a"
		if s.scanEst > 0 {
			underestimate = fmt.Sprintf("%.1fx", s.scanActual/s.scanEst)
		}
		fmt.Printf("%-8d %-16d %-16.0f %-16.0f %-20s\n", s.q, s.clauses, s.scanEst, s.scanActual, underestimate)
	}
	fmt.Println()

	return nil
}
