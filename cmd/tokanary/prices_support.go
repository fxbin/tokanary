package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fxbin/tokanary/internal/clisession"
	"github.com/fxbin/tokanary/internal/pidata"
	"github.com/fxbin/tokanary/internal/pricing"
	"github.com/fxbin/tokanary/internal/warehouse"
)

// curatedDoc is the on-disk shape of .cache/prices-raw.json. It is written so the
// result loads back through pricing.LoadCurated unchanged.
type curatedDoc struct {
	FetchedAt string         `json:"fetchedAt"`
	Source    string         `json:"source"`
	Note      string         `json:"note"`
	Models    []curatedModel `json:"models"`
}

type curatedModel struct {
	Target      string           `json:"target"`
	DisplayName string           `json:"displayName"`
	ModelsDevID string           `json:"modelsDevId"`
	Provider    string           `json:"provider"`
	PiIDs       []string         `json:"piIds"`
	Cost        pricing.Cost     `json:"cost"`
	Confidence  string           `json:"confidence"`
	SourceURL   string           `json:"sourceUrl"`
	Note        string           `json:"note"`
	Variants    []curatedVariant `json:"variants,omitempty"`
}

type curatedVariant struct {
	Label    string       `json:"label"`
	Provider string       `json:"provider"`
	Cost     pricing.Cost `json:"cost"`
}

type priceRow struct {
	target      string
	modelsDevID string
	provider    string
	piIDs       []string
	cost        pricing.Cost
	confidence  string
	sourceURL   string
}

func mustRead(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// hasFlag reports whether a bare switch is present. Used for the opt-outs that
// only make sense per-invocation (`--no-prices`) rather than as fields on the
// opts struct.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// piModelIDs lists the distinct model ids present in the pi warehouse. It goes
// through the same snapshot + warehouse path as `tokanary build` so the live
// pi.sqlite is never opened for writing - build's warehouse.DB deletes the db
// file it is given, and handing it the live source would fail while pi is
// running.
func piModelIDs(piDir, workDir string) ([]string, error) {
	src, err := resolveSource(piDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	snap, err := pidata.Snapshot(src.Dir, workDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := pidata.RemoveSnapshot(snap); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] 娓呯悊 pi 蹇収鐩綍澶辫触: %v\n", err)
		}
	}()

	data, err := pidata.Read(snap, src.DBPath)
	if err != nil {
		return nil, err
	}
	cliDirs := defaultCLIDirs()
	var cli *clisession.Result
	if len(cliDirs) > 0 {
		exclude := map[string]bool{}
		for _, s := range data.Sessions {
			exclude[s.ID] = true
		}
		res := clisession.Collect(cliDirs, exclude)
		if len(res.Sessions) > 0 || len(res.Turns) > 0 {
			cli = &res
		}
	}

	dbPath := filepath.Join(workDir, "tokanary-prices.sqlite")
	db, err := warehouse.DB(dbPath, data, cli)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT DISTINCT model_canon FROM turns WHERE model_canon <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}
