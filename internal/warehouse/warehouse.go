package warehouse

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/pidata"

	_ "modernc.org/sqlite"
)

// TopSessions caps the sessions slice in the payload; sessionsAll keeps the
// rest. Mirrors TOP_SESSIONS in build_data.py.
const TopSessions = 40

// DB builds the warehouse from a normalised source plus optional CLI rows.
func DB(path string, d *pidata.Data, cli *clisession.Result) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// drop the old file plus its WAL/SHM siblings, matching python's unlink pair
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(Schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("寤?schema: %w", err)
	}
	if err := insert(db, d, cli); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func insert(db *sql.DB, d *pidata.Data, cli *clisession.Result) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(d.Turns) > 0 {
		st, err := tx.Prepare(`INSERT INTO turns
            (session_id, tool, model_raw, model_canon, started_at, ended_at, status,
             cache_read, cache_write, input, output, reasoning, total, has_usage)
            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		for _, r := range d.Turns {
			if _, err := st.Exec(r.SessionID, r.Tool, r.ModelRaw, r.ModelCanon,
				r.StartedAt, r.EndedAt, r.Status, r.CacheRead, r.CacheWrite,
				r.Input, r.Output, r.Reasoning, r.Total, r.HasUsage); err != nil {
				st.Close()
				return err
			}
		}
		st.Close()
	}

	if len(d.Sessions) > 0 {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO sessions
            (id, title, project, model_raw, mode, thinking, created_at, updated_at, turns, messages)
            VALUES (?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		for _, r := range d.Sessions {
			if _, err := st.Exec(r.ID, r.Title, r.Project, r.ModelRaw, r.Mode,
				r.Thinking, r.CreatedAt, r.UpdatedAt, r.Turns, r.Messages); err != nil {
				st.Close()
				return err
			}
		}
		st.Close()
	}

	if len(d.Projects) > 0 {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO projects(id, name, path) VALUES (?,?,?)`)
		if err != nil {
			return err
		}
		for _, r := range d.Projects {
			// projects.id is TEXT so the CLI ingest can key by cwd; the
			// sqlite-side ids are integers, so stringify them the same way
			// python's varchar column stored them
			if _, err := st.Exec(strconv.FormatInt(r.ID, 10), r.Name, r.Path); err != nil {
				st.Close()
				return err
			}
		}
		st.Close()
	}

	for _, tbl := range []struct {
		name  string
		inset string
		order string
		data  map[string][2]int64
	}{
		{"roles_agg", "role", `SELECT role, n, errors FROM roles_agg ORDER BY role`, d.RoleCounts},
		{"tools_agg", "name", `SELECT name, n, errors FROM tools_agg ORDER BY n DESC`, d.ToolCounts},
	} {
		if err := insertCounts(tx, tbl.name, tbl.inset, tbl.order, tbl.data); err != nil {
			return err
		}
	}

	// CLI JSONL rows land in the same tables, tagged tool='pi-cli'. The AGGS
	// SQL is unchanged, which is the whole point of the unified schema.
	if cli != nil {
		if len(cli.Turns) > 0 {
			st, err := tx.Prepare(`INSERT INTO turns
                (session_id, tool, model_raw, model_canon, started_at, ended_at, status,
                 cache_read, cache_write, input, output, reasoning, total, has_usage)
                VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
			if err != nil {
				return err
			}
			for _, r := range cli.Turns {
				if _, err := st.Exec(r.Session, "pi-cli", r.ModelRaw, r.ModelCanon,
					r.StartedAt, r.EndedAt, r.Status, r.CacheRead, r.CacheWrite,
					r.Input, r.Output, r.Reasoning, r.Total, r.HasUsage); err != nil {
					st.Close()
					return err
				}
			}
			st.Close()
		}
		if len(cli.Sessions) > 0 {
			st, err := tx.Prepare(`INSERT OR REPLACE INTO sessions
                (id, title, project, model_raw, mode, thinking, created_at, updated_at, turns, messages)
                VALUES (?,?,?,?,?,?,?,?,?,?)`)
			if err != nil {
				return err
			}
			for _, r := range cli.Sessions {
				if _, err := st.Exec(r.ID, "(无标题)", r.Project, r.ModelRaw, r.Mode,
					r.Thinking, r.CreatedAt, r.UpdatedAt, r.Turns, r.Messages); err != nil {
					st.Close()
					return err
				}
			}
			st.Close()
		}
		// CLI role/tool counts are additive onto the sqlite-side aggregate.
		if err := mergeCounts(tx, "roles_agg", "role", "role", cli.Roles); err != nil {
			return err
		}
		if err := mergeCounts(tx, "tools_agg", "name", "name", cli.Tools); err != nil {
			return err
		}
		// CLI projects are keyed by cwd (not the sqlite integer id) and land in
		// the same table, which is why totals.projects grows by one per CLI
		// project. Omitting this silently undercounts it. Here the id already IS
		// the directory, so it doubles as the path the git panel needs.
		for _, cwd := range cli.Projects {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO projects(id, name, path) VALUES (?,?,?)`,
				cwd, filepath.Base(cwd), cwd); err != nil {
				return err
			}
		}
		if err := writeCLIInfo(tx, cli); err != nil {
			return err
		}
	}

	var first, last sql.NullInt64
	if err := tx.QueryRow(Aggs["range"]).Scan(&first, &last); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO meta_info VALUES ('dbVersion',?),('firstTs',?),('lastTs',?),('msgCount',?)`,
		d.DBVersion, nullInt(first), nullInt(last), d.MsgCount); err != nil {
		return err
	}
	return tx.Commit()
}

func insertCounts(tx *sql.Tx, table, col, _ string, data map[string][2]int64) error {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	st, err := tx.Prepare(`INSERT INTO ` + table + `(` + col + `, n, errors) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, k := range keys {
		if _, err := st.Exec(k, data[k][0], data[k][1]); err != nil {
			return err
		}
	}
	return nil
}

// mergeCounts adds the CLI aggregate onto the already-inserted sqlite rows,
// keeping whichever order the caller inserted.
func mergeCounts(tx *sql.Tx, table, col, _ string, add map[string][2]int) error {
	keys := make([]string, 0, len(add))
	for k := range add {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := tx.Exec(
			`INSERT INTO `+table+`(`+col+`, n, errors) VALUES (?,?,?)
             ON CONFLICT DO UPDATE SET n = n + excluded.n, errors = errors + excluded.errors`,
			k, add[k][0], add[k][1]); err != nil {
			return err
		}
	}
	return nil
}

func writeCLIInfo(tx *sql.Tx, cli *clisession.Result) error {
	total := int64(0)
	for _, t := range cli.Turns {
		total += t.Total
	}
	pairs := [][2]string{
		{"dirs", strings.Join(cli.Dirs, "; ")},
		{"files", fmt.Sprint(cli.Files)},
		{"sessions", fmt.Sprint(len(cli.Sessions))},
		{"turns", fmt.Sprint(len(cli.Turns))},
		{"skippedDup", fmt.Sprint(cli.SkippedDup)},
		{"skippedEmpty", fmt.Sprint(cli.SkippedEmpty)},
		{"tokenTotal", fmt.Sprint(total)},
	}
	for _, p := range pairs {
		if _, err := tx.Exec(`INSERT INTO cli_info VALUES (?,?)`, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

func nullInt(v sql.NullInt64) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}

// ModelStats is one entry of the models map.
type ModelStats struct {
	RawIDs      []string
	Turns       int64
	MissingUage int64
	Statuses    map[string]int64
	FirstTs     sql.NullInt64
	LastTs      sql.NullInt64
	RawUsage    map[string]TokenStats
	CacheRead   int64
	CacheWrite  int64
	Input       int64
	Output      int64
	Reasoning   int64
	Total       int64
}

// TokenStats is one raw-id usage breakdown.
type TokenStats struct {
	Turns      int64 `json:"turns"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
}

// Totals is the payload totals block.
type Totals struct {
	Turns          int64   `json:"turns"`
	TurnsWithUsage int64   `json:"turnsWithUsage"`
	CacheRead      int64   `json:"cacheRead"`
	CacheWrite     int64   `json:"cacheWrite"`
	Input          int64   `json:"input"`
	Output         int64   `json:"output"`
	Reasoning      int64   `json:"reasoning"`
	Total          int64   `json:"total"`
	Sessions       int64   `json:"sessions"`
	Messages       int64   `json:"messages"`
	Projects       int64   `json:"projects"`
	CacheHitPct    float64 `json:"cacheHitPct"`
}

// DayRow is one daily bucket.
type DayRow struct {
	D          string `json:"d"`
	Turns      int64  `json:"turns"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	Reasoning  int64  `json:"reasoning"`
	Total      int64  `json:"total"`
}

// DayModelRow is one day x model bucket.
type DayModelRow struct {
	D      string `json:"d"`
	Key    string `json:"key"`
	Turns  int64  `json:"turns"`
	Total  int64  `json:"total"`
	Output int64  `json:"output"`
}

// HourRow is one hourly bucket. H keeps the "YYYY-MM-DD HH" contract.
type HourRow struct {
	H          string `json:"h"`
	Turns      int64  `json:"turns"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	Total      int64  `json:"total"`
}

// ProjectRow is one project bucket.
type ProjectRow struct {
	Name       string `json:"name"`
	Sessions   int64  `json:"sessions"`
	Turns      int64  `json:"turns"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	Reasoning  int64  `json:"reasoning"`
	Total      int64  `json:"total"`
}

// SessionRow is one session usage row.
type SessionRow struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Project    string  `json:"project"`
	Model      string  `json:"model"`
	Mode       *string `json:"mode"`
	Thinking   *string `json:"thinking"`
	CreatedAt  int64   `json:"createdAt"`
	UpdatedAt  int64   `json:"updatedAt"`
	Turns      int64   `json:"turns"`
	Messages   int64   `json:"messages"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	Total      int64   `json:"total"`
}

// ToolRow is one tool aggregate row.
type ToolRow struct {
	Name   string `json:"name"`
	N      int64  `json:"n"`
	Errors int64  `json:"errors"`
}

// RoleCount is one role's n/errors pair. It marshals as an object because
// data.js carries {n, errors} there, not a positional array.
type RoleCount struct {
	N      int64 `json:"n"`
	Errors int64 `json:"errors"`
}

// Usage is the data.js usage payload. Models stays a map keyed by canonical
// model here; build_data.py converts it to a priced list later, so the parity
// gate compares against export_usage, not the final data.js shape.
type Usage struct {
	Totals       Totals                 `json:"totals"`
	Models       map[string]*ModelStats `json:"models"`
	Days         []DayRow               `json:"days"`
	DayModel     []DayModelRow          `json:"dayModel"`
	Hours        []HourRow              `json:"hours"`
	Projects     []ProjectRow           `json:"projects"`
	SessionsAll  []SessionRow           `json:"sessionsAll"`
	Sessions     []SessionRow           `json:"sessions"`
	SessionCount int                    `json:"sessionCount"`
	Roles        map[string]RoleCount   `json:"roles"`
	Tools        []ToolRow              `json:"tools"`
	FirstTs      sql.NullInt64          `json:"firstTs"`
	LastTs       sql.NullInt64          `json:"lastTs"`
	DBVersion    sql.NullInt64          `json:"dbVersion"`

	// ProjectPaths maps a project name to its working directory, for the git
	// panel. It is a lookup table rather than payload: nothing on the dashboard
	// renders it, and keeping it out of the JSON stops it from leaking into the
	// parity comparison against the reference payload.
	ProjectPaths map[string]string `json:"-"`
}

// ExportUsage runs the aggregates and assembles the usage payload.
func ExportUsage(db *sql.DB) (*Usage, error) {
	u := &Usage{Models: map[string]*ModelStats{}, Roles: map[string]RoleCount{}}

	var t struct {
		turns, withUsage, cr, cw, in, out, re, tot sql.NullInt64
	}
	row := db.QueryRow(Aggs["totals"])
	if err := row.Scan(&t.turns, &t.withUsage, &t.cr, &t.cw, &t.in, &t.out, &t.re, &t.tot); err != nil {
		return nil, fmt.Errorf("totals: %w", err)
	}
	mi, err := metaInfo(db)
	if err != nil {
		return nil, err
	}
	var sessCount, projCount int64
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessCount); err != nil {
		return nil, err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&projCount); err != nil {
		return nil, err
	}
	msgCount := mi.msgCount
	denom := max64(1, t.cr.Int64+t.cw.Int64+t.in.Int64)
	u.Totals = Totals{
		Turns: t.turns.Int64, TurnsWithUsage: t.withUsage.Int64,
		CacheRead: t.cr.Int64, CacheWrite: t.cw.Int64, Input: t.in.Int64,
		Output: t.out.Int64, Reasoning: t.re.Int64, Total: t.tot.Int64,
		Sessions: sessCount, Messages: msgCount, Projects: projCount,
		CacheHitPct: round1(100.0 * float64(t.cr.Int64) / float64(denom)),
	}

	if err := scanModels(db, u); err != nil {
		return nil, err
	}
	if err := scanModelRaw(db, u); err != nil {
		return nil, err
	}
	if err := scanModelStatus(db, u); err != nil {
		return nil, err
	}
	if u.Days, err = scanDays(db); err != nil {
		return nil, err
	}
	if u.DayModel, err = scanDayModel(db); err != nil {
		return nil, err
	}
	if u.Hours, err = scanHours(db); err != nil {
		return nil, err
	}
	if u.Projects, err = scanProjects(db); err != nil {
		return nil, err
	}
	if u.ProjectPaths, err = scanProjectPaths(db); err != nil {
		return nil, err
	}
	if u.SessionsAll, err = scanSessions(db); err != nil {
		return nil, err
	}
	u.SessionCount = len(u.SessionsAll)
	u.Sessions = u.SessionsAll
	if len(u.Sessions) > TopSessions {
		u.Sessions = u.Sessions[:TopSessions]
	}
	if u.Roles, err = scanRoles(db); err != nil {
		return nil, err
	}
	if u.Tools, err = scanTools(db); err != nil {
		return nil, err
	}
	u.FirstTs, u.LastTs, u.DBVersion = mi.firstTs, mi.lastTs, mi.dbVersion
	return u, nil
}

type meta struct {
	dbVersion sql.NullInt64
	firstTs   sql.NullInt64
	lastTs    sql.NullInt64
	msgCount  int64
}

func metaInfo(db *sql.DB) (meta, error) {
	var m meta
	rows, err := db.Query("SELECT k, v FROM meta_info")
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v sql.NullInt64
		if err := rows.Scan(&k, &v); err != nil {
			return m, err
		}
		switch k {
		case "dbVersion":
			m.dbVersion = v
		case "firstTs":
			m.firstTs = v
		case "lastTs":
			m.lastTs = v
		case "msgCount":
			m.msgCount = v.Int64
		}
	}
	return m, rows.Err()
}

func scanModels(db *sql.DB, u *Usage) error {
	rows, err := db.Query(Aggs["models"])
	if err != nil {
		return fmt.Errorf("models: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var turns, missing, cr, cw, in, out, re, tot sql.NullInt64
		var rawIds, status sql.NullString
		var firstTs, lastTs sql.NullInt64
		if err := rows.Scan(&key, &turns, &missing, &cr, &cw, &in, &out, &re, &tot,
			&rawIds, &firstTs, &lastTs); err != nil {
			return err
		}
		var ids []string
		if rawIds.Valid && rawIds.String != "" {
			seen := map[string]bool{}
			for _, s := range strings.Split(rawIds.String, "\x1f") {
				if s != "" && !seen[s] {
					seen[s] = true
					ids = append(ids, s)
				}
			}
		}
		u.Models[key] = &ModelStats{
			RawIDs: ids, Turns: turns.Int64, MissingUage: missing.Int64,
			Statuses: map[string]int64{}, FirstTs: firstTs, LastTs: lastTs,
			RawUsage:  map[string]TokenStats{},
			CacheRead: cr.Int64, CacheWrite: cw.Int64, Input: in.Int64,
			Output: out.Int64, Reasoning: re.Int64, Total: tot.Int64,
		}
		_ = status
	}
	return rows.Err()
}

func scanModelRaw(db *sql.DB, u *Usage) error {
	rows, err := db.Query(Aggs["modelRaw"])
	if err != nil {
		return fmt.Errorf("modelRaw: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, rid string
		var turns, cr, cw, in, out, re, tot int64
		if err := rows.Scan(&key, &rid, &turns, &cr, &cw, &in, &out, &re, &tot); err != nil {
			return err
		}
		if m := u.Models[key]; m != nil {
			m.RawUsage[rid] = TokenStats{Turns: turns, CacheRead: cr, CacheWrite: cw,
				Input: in, Output: out, Reasoning: re, Total: tot}
		}
	}
	return rows.Err()
}

func scanModelStatus(db *sql.DB, u *Usage) error {
	rows, err := db.Query(Aggs["modelStatus"])
	if err != nil {
		return fmt.Errorf("modelStatus: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var status sql.NullString
		var n int64
		if err := rows.Scan(&key, &status, &n); err != nil {
			return err
		}
		if m := u.Models[key]; m != nil {
			m.Statuses[status.String] = n
		}
	}
	return rows.Err()
}

func scanDays(db *sql.DB) ([]DayRow, error) {
	rows, err := db.Query(Aggs["days"])
	if err != nil {
		return nil, fmt.Errorf("days: %w", err)
	}
	defer rows.Close()
	out := []DayRow{}
	for rows.Next() {
		var d sql.NullString
		var r DayRow
		if err := rows.Scan(&d, &r.Turns, &r.CacheRead, &r.CacheWrite, &r.Input,
			&r.Output, &r.Reasoning, &r.Total); err != nil {
			return nil, err
		}
		r.D = d.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanDayModel(db *sql.DB) ([]DayModelRow, error) {
	rows, err := db.Query(Aggs["dayModel"])
	if err != nil {
		return nil, fmt.Errorf("dayModel: %w", err)
	}
	defer rows.Close()
	out := []DayModelRow{}
	for rows.Next() {
		var d sql.NullString
		var r DayModelRow
		if err := rows.Scan(&d, &r.Key, &r.Turns, &r.Total, &r.Output); err != nil {
			return nil, err
		}
		r.D = d.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanHours(db *sql.DB) ([]HourRow, error) {
	rows, err := db.Query(Aggs["hours"])
	if err != nil {
		return nil, fmt.Errorf("hours: %w", err)
	}
	defer rows.Close()
	out := []HourRow{}
	for rows.Next() {
		var h sql.NullString
		var r HourRow
		if err := rows.Scan(&h, &r.Turns, &r.CacheRead, &r.CacheWrite, &r.Input,
			&r.Output, &r.Total); err != nil {
			return nil, err
		}
		r.H = h.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanProjects(db *sql.DB) ([]ProjectRow, error) {
	rows, err := db.Query(Aggs["projects"])
	if err != nil {
		return nil, fmt.Errorf("projects: %w", err)
	}
	defer rows.Close()
	out := []ProjectRow{}
	for rows.Next() {
		var r ProjectRow
		if err := rows.Scan(&r.Name, &r.Sessions, &r.Turns, &r.CacheRead,
			&r.CacheWrite, &r.Input, &r.Output, &r.Reasoning, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// python sorts descending by total here, not in SQL
	sort.SliceStable(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out, rows.Err()
}

// scanProjectPaths reads the name -> working directory table the git panel
// needs. Rows without a path are skipped rather than stored as empty strings:
// an empty root would make ProjectGit look at the current directory, and a
// project whose path is unknown is not the same thing as one at "".
//
// A warehouse written before the path column existed is tolerated, and only
// that. The read path serves the dashboard from this file, so failing here on an
// old artifact would blank the whole page until the next refresh; the git panel
// simply goes back to being empty, which is what it did before the column
// landed. Every other error still propagates, so a genuine query bug is not
// hidden behind the same tolerance.
func scanProjectPaths(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, path FROM projects
        WHERE path IS NOT NULL AND path <> ''`)
	if err != nil {
		if strings.Contains(err.Error(), "no such column") {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("project paths: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, path sql.NullString
		if err := rows.Scan(&name, &path); err != nil {
			return nil, err
		}
		if name.String != "" && path.String != "" {
			out[name.String] = path.String
		}
	}
	return out, rows.Err()
}

func scanSessions(db *sql.DB) ([]SessionRow, error) {
	rows, err := db.Query(Aggs["sessionsUsage"])
	if err != nil {
		return nil, fmt.Errorf("sessionsUsage: %w", err)
	}
	defer rows.Close()
	out := []SessionRow{}
	for rows.Next() {
		var id string
		var title, project, modelRaw, mode, thinking sql.NullString
		var created, updated, turns, msgs, cr, cw, in, out_, tot int64
		if err := rows.Scan(&id, &title, &project, &modelRaw, &mode, &thinking,
			&created, &updated, &turns, &msgs, &cr, &cw, &in, &out_, &tot); err != nil {
			return nil, err
		}
		t := title.String
		if t == "" {
			t = "(无标题)"
		}
		p := project.String
		if p == "" {
			p = "(无项目)"
		}
		s := SessionRow{
			ID: id, Title: t, Project: p,
			Model:     clisession.CanonicalModel(modelRaw.String),
			CreatedAt: created, UpdatedAt: updated, Turns: turns, Messages: msgs,
			CacheRead: cr, CacheWrite: cw, Input: in, Output: out_, Total: tot,
		}
		if mode.Valid {
			m := mode.String
			s.Mode = &m
		}
		if thinking.Valid {
			th := thinking.String
			s.Thinking = &th
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanRoles(db *sql.DB) (map[string]RoleCount, error) {
	rows, err := db.Query(Aggs["roles"])
	if err != nil {
		return nil, fmt.Errorf("roles: %w", err)
	}
	defer rows.Close()
	out := map[string]RoleCount{}
	for rows.Next() {
		var role string
		var n, e int64
		if err := rows.Scan(&role, &n, &e); err != nil {
			return nil, err
		}
		out[role] = RoleCount{N: n, Errors: e}
	}
	return out, rows.Err()
}

func scanTools(db *sql.DB) ([]ToolRow, error) {
	rows, err := db.Query(Aggs["tools"])
	if err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}
	defer rows.Close()
	out := []ToolRow{}
	for rows.Next() {
		var r ToolRow
		if err := rows.Scan(&r.Name, &r.N, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CLIStats returns the cli_info table, for the meta.cli block.
func CLIStats(db *sql.DB) map[string]string {
	rows, err := db.Query("SELECT k, v FROM cli_info")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return out
		}
		out[k] = v
	}
	return out
}

// LoadDashboardPayload parses a data.js file into generic maps for the parity gate.
func LoadDashboardPayload(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	body := string(raw)
	i := strings.Index(body, "=")
	if i < 0 {
		return nil, fmt.Errorf("%s: 不是 data.js 格式", path)
	}
	body = strings.TrimSpace(body[i+1:])
	body = strings.TrimSuffix(body, ";")
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// round1 matches python's round(x, 1) for the cache-hit percentage.
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
