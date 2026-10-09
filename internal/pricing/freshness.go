package pricing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultPriceMaxAge is how long a generated price table stays current.
//
// Seven days is a judgement call about the upstream catalogue, not about this
// project: models get added, renamed and repriced on that timescale, and a
// table older than a week starts missing models the user is actually running -
// which does not announce itself, it just quietly prices those models at $0.
//
// The cost of refreshing is one ~5 MB GET per week, once, off the critical path.
// The cost of NOT refreshing is a dashboard that under-reports and looks fine.
const DefaultPriceMaxAge = 7 * 24 * time.Hour

// PriceTableAge returns how old a `fetchedAt` date is. The bool reports whether
// the date was usable; a table with an unparseable stamp is treated as stale by
// the caller rather than as infinitely fresh, because an unreadable date must
// never be the reason a stale table is kept forever.
func PriceTableAge(fetchedAt any, now time.Time) (time.Duration, bool) {
	s, ok := fetchedAt.(string)
	if !ok || len(s) < 10 {
		return 0, false
	}
	d, err := time.ParseInLocation("2006-01-02", s[:10], now.Location())
	if err != nil {
		return 0, false
	}
	// Stamp is a date, not an instant: compare against the start of today so a
	// table generated this morning does not read as several hours stale.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return today.Sub(d), true
}

// PricesStatus describes the local price table's freshness.
type PricesStatus struct {
	Path      string        // the table that was inspected
	Exists    bool          // a table is on disk
	FetchedAt string        // the table's own stamp, verbatim
	Age       time.Duration // how old it is
	Stale     bool          // Age exceeds maxAge, or the stamp was unreadable
	Reason    string        // why it is stale, in words the UI can show
}

// InspectPrices reads a generated table's stamp without loading its models, so
// the freshness check stays cheap enough to run on every collect.
func InspectPrices(path string, maxAge time.Duration, now time.Time) PricesStatus {
	st := PricesStatus{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		// A missing table counts as stale, not as "nothing to do here". A caller
		// that checks only Stale would otherwise read "no table at all" as
		// "perfectly current" and leave every cost at $0 - the exact failure the
		// freshness check exists to prevent. Callers that care about the
		// difference can still read Exists.
		st.Stale = true
		st.Reason = "没有价表，全部金额按 $0 计"
		return st
	}
	st.Exists = true
	var doc struct {
		FetchedAt any `json:"fetchedAt"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		st.Stale = true
		st.Reason = "价表无法解析"
		return st
	}
	if s, ok := doc.FetchedAt.(string); ok {
		st.FetchedAt = s
	}
	age, ok := PriceTableAge(doc.FetchedAt, now)
	if !ok {
		st.Stale = true
		st.Reason = "价表没有可读的抓取日期"
		return st
	}
	st.Age = age
	st.Stale = age > maxAge
	if st.Stale {
		st.Reason = fmt.Sprintf("价表已 %d 天未更新", int(age.Hours()/24))
	}
	return st
}

// AtomicWriteFile replaces dst with data via a same-directory temp file and a
// rename.
//
// The price table is about to be rewritten without a human watching, so a crash
// mid-write must not be able to leave a truncated table behind: every later run
// would then price against a partial catalogue and the dashboard would under-
// report with no way to tell. The old table stays intact until the rename lands.
func AtomicWriteFile(dst string, data []byte) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}
