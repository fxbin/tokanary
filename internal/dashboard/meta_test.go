package dashboard

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fxbin/tokanary/internal/warehouse"
)

// metaFromFixture assembles a payload against a throwaway repo root whose
// warehouse file has an exactly known size, and returns the meta block.
func metaFromFixture(t *testing.T, dbBytes, walBytes int64, dbVersion int64) *struct {
	DBSizeMB  float64
	WALSizeMB float64
	DBVersion int64
	DBPath    string
} {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("mkdir .cache: %v", err)
	}
	db := filepath.Join(root, DefaultDBPath)
	if err := os.WriteFile(db, make([]byte, dbBytes), 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if walBytes > 0 {
		if err := os.WriteFile(db+"-wal", make([]byte, walBytes), 0o644); err != nil {
			t.Fatalf("write wal: %v", err)
		}
	}
	usage := &warehouse.Usage{}
	if dbVersion > 0 {
		usage.DBVersion = sql.NullInt64{Int64: dbVersion, Valid: true}
	}
	payload, err := AssembleFromUsage(Options{RepoRoot: root, NoExternal: true}, usage, nil)
	if err != nil {
		t.Fatalf("AssembleFromUsage: %v", err)
	}
	m := payload.Meta
	return &struct {
		DBSizeMB  float64
		WALSizeMB float64
		DBVersion int64
		DBPath    string
	}{m.DBSizeMB, m.WALSizeMB, m.DBVersion, m.DBPath}
}

// 仓库体积以前在桌面路径上硬传 0，而同一段代码生成的 dbPath 标签却写着
// 「本地仓库（0.3 MB）」—— 同一个 meta 块里两个数字互相打架，而结构化字段
// dbSizeMB 才是读 payload 的人真正会取的那个。修好后必须等于真实字节数。
func TestMetaDBSizeIsRealNotHardcodedZero(t *testing.T) {
	const mb = 1024 * 1024
	cases := []struct {
		name              string
		db, wal           int64
		wantDB, wantWALMB float64
	}{
		{"2.5MB 仓库", 2621440, 0, 2.5, 0},
		{"带 WAL", 3145728, 1048576, 3, 1},
		{"小于 0.1MB 不该显示成 0 而被当成没有", 61440, 0, 0.1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := metaFromFixture(t, c.db, c.wal, 7)
			if m.DBSizeMB != c.wantDB {
				t.Errorf("DBSizeMB = %v, want %v (%d bytes)", m.DBSizeMB, c.wantDB, c.db)
			}
			if m.WALSizeMB != c.wantWALMB {
				t.Errorf("WALSizeMB = %v, want %v (%d bytes)", m.WALSizeMB, c.wantWALMB, c.wal)
			}
			// 标签与结构化字段说的是同一件事，两者不许打架
			wantLabel := fmt.Sprintf("本地仓库（%.1f MB）", float64(c.db)/1048576)
			if m.DBPath != wantLabel {
				t.Errorf("DBPath 标签 = %q, want %q", m.DBPath, wantLabel)
			}
		})
	}
}

// schema 版本以前同样硬传 0。仓库的 meta_info.dbVersion 里存着真实值——
// 那是 pidata.Read 从 pi 源库 pragma user_version 读来抄进去的（注意它和
// 仓库自己的 pragma user_version 不是一回事，后者一直是 0）。
func TestMetaDBVersionComesFromWarehouse(t *testing.T) {
	m := metaFromFixture(t, 1048576, 0, 42)
	if m.DBVersion != 42 {
		t.Errorf("DBVersion = %d, want 42", m.DBVersion)
	}
	// 仓库没记版本时才是 0，不能反过来把 0 当成一个已知版本报出去
	m0 := metaFromFixture(t, 1048576, 0, 0)
	if m0.DBVersion != 0 {
		t.Errorf("DBVersion = %d, want 0 when warehouse has no user_version", m0.DBVersion)
	}
}

// 仓库文件不存在时不能编出一个体积，静默留 0 即可。
func TestMetaDBSizeZeroWhenWarehouseMissing(t *testing.T) {
	root := t.TempDir()
	payload, err := AssembleFromUsage(Options{RepoRoot: root, NoExternal: true}, &warehouse.Usage{}, nil)
	if err != nil {
		t.Fatalf("AssembleFromUsage: %v", err)
	}
	if payload.Meta.DBSizeMB != 0 {
		t.Errorf("DBSizeMB = %v, want 0 when no warehouse file", payload.Meta.DBSizeMB)
	}
	if payload.Meta.DBPath != filepath.Join(".cache", "tokanary.sqlite") {
		t.Errorf("DBPath = %q, want the plain path when no warehouse file", payload.Meta.DBPath)
	}
}
