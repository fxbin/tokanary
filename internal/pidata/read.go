// Package pidata reads the pi desktop sqlite source and normalises it into
// warehouse rows. It mirrors the original python collector and warehouse.
//
// Two deliberate differences from the python original:
//
//   - d/h are no longer materialised columns. python stored them because
//     DuckDB falls back to UTC for the session timezone on Windows
//     (the original duckback-era comment). SQLite's 'localtime' uses the OS zone, the same
//     semantics as python's datetime.fromtimestamp, so the columns are pure
//     redundancy (23 chars/turn, ~20% of the row payload). They are derived in
//     SQL instead. NULL (started_at IS NULL or 0) stays NULL so day/hour
//     grouping is byte-identical to python.
//   - The consistent snapshot uses VACUUM INTO, the pure-Go equivalent of
//     python's sqlite3 Connection.backup(). It produces a commit-boundary
//     clean copy so a live rewrite cannot yield a torn view, but it is ~10x
//     slower than python's backup (4.5s vs 0.45s on a 130MB source) because
//     modernc rebuilds the file instead of copying pages.
package pidata

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fxbin/tokanary/internal/clisession"
)

// DBName is the sqlite file pi writes inside its data dir.
const DBName = "pi.sqlite"

// Source describes one detected pi data directory.
type Source struct {
	Dir      string
	Label    string
	DBPath   string
	DBSize   int64
	WALSize  int64
	Modified int64
}

// PiDirLabel mirrors pi_common.pi_dir_label: the two known dirs get a role
// label, anything else falls back to its own name.
func PiDirLabel(dir string) string {
	switch filepath.Base(dir) {
	case ".pi-desktop":
		return "pi-desktop"
	case ".pi":
		return "pi"
	}
	base := filepath.Base(dir)
	if base != "." && base != string(filepath.Separator) {
		return base
	}
	return dir
}

// DetectPiDirs returns every dir holding a pi.sqlite, newest write first.
// `.pi` and `.pi-desktop` overlap, so the caller takes only the newest one -
// they must never be summed.
func DetectPiDirs() []Source {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	var cands []string
	if env := os.Getenv("PI_HOME"); env != "" {
		cands = append(cands, env)
	}
	if home != "" {
		cands = append(cands,
			filepath.Join(home, ".pi-desktop"),
			filepath.Join(home, ".pi"),
			filepath.Join(home, ".config", "pi"),
			filepath.Join(home, "AppData", "Roaming", "pi"),
		)
	}
	cands = append(cands, `C:\pi`)

	var out []Source
	for _, c := range cands {
		db := filepath.Join(c, DBName)
		stat, err := os.Stat(db)
		if err != nil {
			continue
		}
		src := Source{Dir: c, Label: PiDirLabel(c), DBPath: db, DBSize: stat.Size()}
		if s, err := os.Stat(db + "-wal"); err == nil {
			src.WALSize = s.Size()
			if s.ModTime().UnixMilli() > src.Modified {
				src.Modified = s.ModTime().UnixMilli()
			}
		}
		out = append(out, src)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out
}

// Snapshot copies the source db to work/pi-snapshot/pi.sqlite at a commit
// boundary. Direct file copies of db+-wal+-shm can read a torn view while pi
// is rewriting, which was measured to drop rows; this cannot.
func Snapshot(srcDir, work string) (string, error) {
	src := filepath.Join(srcDir, DBName)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("找不到数据库: %s", src)
	}
	tmp := filepath.Join(work, "pi-snapshot")
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(tmp, DBName)

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(src)+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer db.Close()
	// single-quote the path; SQLITE does not accept a bound param here
	escaped := filepath.ToSlash(dst)
	if _, err := db.Exec(`VACUUM INTO '` + escaped + `'`); err != nil {
		return "", fmt.Errorf("快照失败: %w", err)
	}
	return dst, nil
}

// SessionRow is one warehouse sessions row.
type SessionRow struct {
	ID        string
	Title     string
	Project   string
	ModelRaw  string
	Mode      string
	Thinking  sql.NullString
	CreatedAt int64
	UpdatedAt int64
	Turns     int64
	Messages  int64
}

// TurnRow is one warehouse turns row. d/h are gone on purpose - see the
// package doc.
type TurnRow struct {
	SessionID  string
	Tool       string
	ModelRaw   string
	ModelCanon string
	StartedAt  int64
	EndedAt    int64
	Status     string
	CacheRead  int64
	CacheWrite int64
	Input      int64
	Output     int64
	Reasoning  int64
	Total      int64
	HasUsage   bool
}

// ProjectRow is one warehouse projects row.
type ProjectRow struct {
	ID   int64
	Name string
}

// Data is the fully normalised source, ready to write into the warehouse.
type Data struct {
	DBVersion  int64
	SrcPath    string
	Projects   []ProjectRow
	Sessions   []SessionRow
	Turns      []TurnRow
	RoleCounts map[string][2]int64
	ToolCounts map[string][2]int64
	MsgCount   int64
}

// Read normalises a snapshot into warehouse rows.
func Read(snapPath, srcPath string) (*Data, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snapPath)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	d := &Data{
		SrcPath:    srcPath,
		RoleCounts: map[string][2]int64{},
		ToolCounts: map[string][2]int64{},
	}

	if err := db.QueryRow("pragma user_version").Scan(&d.DBVersion); err != nil {
		return nil, fmt.Errorf("读 user_version: %w", err)
	}

	projRows, err := db.Query("select id, path, name from projects")
	if err != nil {
		return nil, err
	}
	projects := map[int64]string{}
	for projRows.Next() {
		var p ProjectRow
		var path, name sql.NullString
		if err := projRows.Scan(&p.ID, &path, &name); err != nil {
			projRows.Close()
			return nil, err
		}
		p.Name = name.String
		projects[p.ID] = p.Name
		d.Projects = append(d.Projects, p)
	}
	if err := projRows.Err(); err != nil {
		projRows.Close()
		return nil, err
	}
	projRows.Close()

	// python: s.title, projects.get(pid, "(无项目)"), and export_usage applies
	// the same default a second time on the joined side.
	sessRows, err := db.Query(`
		select s.id, s.title, s.project_id, s.model_id, s.mode, s.thinking_level,
		       s.created_at, s.updated_at,
		       (select count(*) from turns t where t.session_id = s.id),
		       (select count(*) from messages m where m.session_id = s.id)
		from sessions s`)
	if err != nil {
		return nil, err
	}
	for sessRows.Next() {
		var r SessionRow
		var title, mode, modelID sql.NullString
		var projectID, createdAt, updatedAt, turns, msgs sql.NullInt64
		if err := sessRows.Scan(&r.ID, &title, &projectID, &modelID, &mode, &r.Thinking,
			&createdAt, &updatedAt, &turns, &msgs); err != nil {
			sessRows.Close()
			return nil, err
		}
		r.Title = title.String
		r.Mode = mode.String
		r.ModelRaw = modelID.String
		// python num() collapses NULL to 0 on every integer column
		r.CreatedAt = createdAt.Int64
		r.UpdatedAt = updatedAt.Int64
		r.Turns = turns.Int64
		r.Messages = msgs.Int64
		if name, ok := projects[projectID.Int64]; ok {
			r.Project = name
		} else {
			r.Project = "(无项目)"
		}
		d.Sessions = append(d.Sessions, r)
	}
	if err := sessRows.Err(); err != nil {
		sessRows.Close()
		return nil, err
	}
	sessRows.Close()

	// usage numbers come out of usage_json; turns.input_tokens holds only the
	// incremental input and would undercount by ~89% (see .agent-memory).
	turnRows, err := db.Query(`
		select session_id, model_id, started_at, ended_at, status,
		       json_extract(usage_json, '$.cacheReadTokens'),
		       json_extract(usage_json, '$.cacheWriteTokens'),
		       json_extract(usage_json, '$.inputTokens'),
		       json_extract(usage_json, '$.outputTokens'),
		       json_extract(usage_json, '$.reasoningTokens'),
		       json_extract(usage_json, '$.totalTokens'),
		       json_extract(usage_json, '$.totalTokens') is null
		from turns`)
	if err != nil {
		return nil, err
	}
	for turnRows.Next() {
		var r TurnRow
		var sessionID, modelID, status sql.NullString
		// ended_at and started_at are both nullable; python's num() maps
		// NULL to 0, so scan through NullInt64 and keep that mapping.
		var startedAt, endedAt sql.NullInt64
		var cr, cw, in, out, re, tot sql.NullInt64
		var missing sql.NullBool
		if err := turnRows.Scan(&sessionID, &modelID, &startedAt, &endedAt, &status,
			&cr, &cw, &in, &out, &re, &tot, &missing); err != nil {
			turnRows.Close()
			return nil, err
		}
		r.SessionID = sessionID.String
		r.Status = status.String
		r.Tool = "pi"
		r.ModelRaw = firstNonEmpty(modelID.String, "(none)")
		r.ModelCanon = clisession.CanonicalModel(r.ModelRaw)
		r.StartedAt = startedAt.Int64
		r.EndedAt = endedAt.Int64
		r.CacheRead = cr.Int64
		r.CacheWrite = cw.Int64
		r.Input = in.Int64
		r.Output = out.Int64
		r.Reasoning = re.Int64
		r.Total = tot.Int64
		r.HasUsage = !(missing.Bool && missing.Valid)
		d.Turns = append(d.Turns, r)
	}
	if err := turnRows.Err(); err != nil {
		turnRows.Close()
		return nil, err
	}
	turnRows.Close()

	if err := db.QueryRow("select count(*) from messages").Scan(&d.MsgCount); err != nil {
		return nil, err
	}
	if err := aggregateCounts(db,
		`select role, count(*), coalesce(sum(is_error),0) from messages group by role`,
		d.RoleCounts); err != nil {
		return nil, err
	}
	if err := aggregateCounts(db,
		`select tool_name, count(*), coalesce(sum(is_error),0) from messages
		 where tool_name is not null and tool_name <> '' group by tool_name`,
		d.ToolCounts); err != nil {
		return nil, err
	}

	return d, nil
}

func aggregateCounts(db *sql.DB, q string, into map[string][2]int64) error {
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name sql.NullString
		var n, e int64
		if err := rows.Scan(&name, &n, &e); err != nil {
			return err
		}
		into[name.String] = [2]int64{n, e}
	}
	return rows.Err()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
