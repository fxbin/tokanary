// Package warehouse writes the analysis store and exports the data.js usage
// payload. It replaces the duckdb-based warehouse it replaces.
//
// Why SQLite: the cgo DuckDB binding needs a C toolchain on every build
// machine and adds ~99MB statically linked. Measured upper bound for one
// person at 12h/day for a year is ~448MB of turns, and every aggregate here
// scans that table in single-digit milliseconds, so the engine is not the
// constraint - toolchain portability is.
package warehouse

// Schema is the store. turns carries no d/h columns; see the pidata package
// doc for why, and Aggs for how day/hour are derived instead.
const Schema = `
CREATE TABLE IF NOT EXISTS turns(
    session_id  TEXT,
    tool        TEXT,
    model_raw   TEXT,
    model_canon TEXT,
    started_at  INTEGER,
    ended_at    INTEGER,
    status      TEXT,
    cache_read  INTEGER,
    cache_write INTEGER,
    input       INTEGER,
    output      INTEGER,
    reasoning   INTEGER,
    total       INTEGER,
    has_usage   INTEGER
);
CREATE TABLE IF NOT EXISTS sessions(
    id         TEXT PRIMARY KEY,
    title      TEXT,
    project    TEXT,
    model_raw  TEXT,
    mode       TEXT,
    thinking   TEXT,
    created_at INTEGER,
    updated_at INTEGER,
    turns      INTEGER,
    messages   INTEGER
);
-- id is TEXT, not INTEGER: the sqlite source uses integer project ids while the
-- CLI ingest keys projects by cwd (a path string), and both land in this one
-- table. python declared this VARCHAR for the same reason.
CREATE TABLE IF NOT EXISTS projects(
    id   TEXT PRIMARY KEY,
    name TEXT
);
-- messages stay aggregated: 144k detail rows would be 100x the turns table and
-- DuckDB/SQLite executemany over them is pathologically slow.
CREATE TABLE IF NOT EXISTS roles_agg(
    role   TEXT,
    n      INTEGER,
    errors INTEGER
);
CREATE TABLE IF NOT EXISTS tools_agg(
    name   TEXT,
    n      INTEGER,
    errors INTEGER
);
CREATE TABLE IF NOT EXISTS meta_info(k TEXT, v INTEGER);
CREATE TABLE IF NOT EXISTS cli_info(k TEXT, v TEXT);

CREATE INDEX IF NOT EXISTS idx_turns_session  ON turns(session_id);
CREATE INDEX IF NOT EXISTS idx_turns_canon    ON turns(model_canon);
CREATE INDEX IF NOT EXISTS idx_turns_started  ON turns(started_at);
CREATE INDEX IF NOT EXISTS idx_sessions_proj  ON sessions(project);
`

// dayExpr and hourExpr replace the dropped d/h columns. started_at is
// milliseconds; a NULL or non-positive value yields NULL so it groups into its
// own bucket exactly as python's `if ts else None` did.
const (
	dayExpr  = `CASE WHEN started_at > 0 THEN date(started_at/1000,'unixepoch','localtime') END`
	hourExpr = `CASE WHEN started_at > 0 THEN strftime('%Y-%m-%d %H', started_at/1000,'unixepoch','localtime') END`
)

// Aggs are the aggregates that produce the data.js usage payload. Only two
// construct changes versus the DuckDB originals:
//
//	STRING_AGG(DISTINCT model_raw, CHR(31))
//	  -> a deduping subquery feeding group_concat(x, char(31)).
//	     SQLite rejects DISTINCT on multi-argument aggregates outright
//	     ("DISTINCT aggregates must have exactly one argument").
//	GROUP BY d / h
//	  -> GROUP BY <derived expression>. COUNT(*) FILTER and MIN() FILTER are
//	     supported by SQLite 3.30+ and were verified unchanged.
//
// This is a var, not a const, because the day/hour queries interpolate
// dayExpr and hourExpr.
var Aggs = map[string]string{
	"totals": `
        SELECT COUNT(*) AS turns,
               COUNT(*) FILTER (WHERE has_usage) AS turns_with_usage,
               SUM(cache_read) AS cacheRead, SUM(cache_write) AS cacheWrite,
               SUM(input) AS input, SUM(output) AS output,
               SUM(reasoning) AS reasoning, SUM(total) AS total
        FROM turns`,
	"models": `
        SELECT model_canon AS key, turns, missingUsage, cacheRead, cacheWrite,
               input, output, reasoning, total, rawIds, firstTs, lastTs
        FROM (
          SELECT model_canon AS model_canon, SUM(n) AS turns,
                 SUM(nMissing) AS missingUsage, SUM(cache_read) AS cacheRead,
                 SUM(cache_write) AS cacheWrite, SUM(input) AS input,
                 SUM(output) AS output, SUM(reasoning) AS reasoning,
                 SUM(total) AS total,
                 group_concat(rid, char(31)) AS rawIds,
                 MIN(firstTs) AS firstTs, MAX(lastTs) AS lastTs
          FROM (
            SELECT model_canon AS model_canon, model_raw AS rid,
                   COUNT(*) AS n,
                   SUM(CASE WHEN NOT has_usage THEN 1 ELSE 0 END) AS nMissing,
                   SUM(cache_read) AS cache_read, SUM(cache_write) AS cache_write,
                   SUM(input) AS input, SUM(output) AS output,
                   SUM(reasoning) AS reasoning, SUM(total) AS total,
                   MIN(CASE WHEN started_at > 0 THEN started_at END) AS firstTs,
                   MAX(started_at) AS lastTs
            FROM turns GROUP BY model_canon, model_raw
          )
          GROUP BY model_canon
        )`,
	"modelRaw": `
        SELECT model_canon AS key, model_raw AS rid, COUNT(*) AS turns,
               SUM(cache_read) AS cacheRead, SUM(cache_write) AS cacheWrite,
               SUM(input) AS input, SUM(output) AS output,
               SUM(reasoning) AS reasoning, SUM(total) AS total
        FROM turns GROUP BY model_canon, model_raw`,
	"modelStatus": `
        SELECT model_canon AS key, status, COUNT(*) AS n
        FROM turns GROUP BY model_canon, status`,
	"days": `
        SELECT d, COUNT(*) AS turns,
               SUM(cache_read) AS cacheRead, SUM(cache_write) AS cacheWrite,
               SUM(input) AS input, SUM(output) AS output,
               SUM(reasoning) AS reasoning, SUM(total) AS total
        FROM (SELECT ` + dayExpr + ` AS d, cache_read, cache_write, input, output, reasoning, total
              FROM turns)
        GROUP BY d ORDER BY d`,
	"dayModel": `
        SELECT d, model_canon AS key, COUNT(*) AS turns,
               SUM(total) AS total, SUM(output) AS output
        FROM (SELECT ` + dayExpr + ` AS d, model_canon, total, output FROM turns)
        GROUP BY d, model_canon ORDER BY d, model_canon`,
	"hours": `
        SELECT h, COUNT(*) AS turns,
               SUM(cache_read) AS cacheRead, SUM(cache_write) AS cacheWrite,
               SUM(input) AS input, SUM(output) AS output, SUM(total) AS total
        FROM (SELECT ` + hourExpr + ` AS h, cache_read, cache_write, input, output, total
              FROM turns)
        WHERE h IS NOT NULL GROUP BY h ORDER BY h`,
	"projects": `
        SELECT COALESCE(s.project, '(无项目)') AS name,
               COUNT(DISTINCT t.session_id) AS sessions,
               COUNT(*) AS turns,
               SUM(t.cache_read) AS cacheRead, SUM(t.cache_write) AS cacheWrite,
               SUM(t.input) AS input, SUM(t.output) AS output,
               SUM(t.reasoning) AS reasoning, SUM(t.total) AS total
        FROM turns t LEFT JOIN sessions s ON s.id = t.session_id
        WHERE t.has_usage GROUP BY COALESCE(s.project, '(无项目)')`,
	"sessionsUsage": `
        SELECT s.id, s.title, s.project, s.model_raw, s.mode, s.thinking,
               s.created_at, s.updated_at, s.turns, s.messages,
               SUM(t.cache_read) AS cacheRead, SUM(t.cache_write) AS cacheWrite,
               SUM(t.input) AS input, SUM(t.output) AS output, SUM(t.total) AS total
        FROM sessions s
        JOIN turns t ON t.session_id = s.id AND t.has_usage
        GROUP BY s.id, s.title, s.project, s.model_raw, s.mode, s.thinking,
                 s.created_at, s.updated_at, s.turns, s.messages
        ORDER BY total DESC`,
	"roles": `SELECT role, n, errors FROM roles_agg`,
	// name is a deterministic tiebreak: duckdb's bare `ORDER BY n DESC` leaves
	// equal counts in an arbitrary order, so without this the Go output is not
	// reproducible run to run (the parity gate matches by name, not position).
	"tools": `SELECT name, n, errors FROM tools_agg ORDER BY n DESC, name`,
	"range": `
        SELECT MIN(started_at) FILTER (WHERE started_at > 0),
               MAX(started_at)
        FROM turns`,
}
