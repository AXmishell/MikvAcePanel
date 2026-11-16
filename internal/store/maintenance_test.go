package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// seededBatchSeq keeps generated stats_batches IDs unique per test process.
var seededBatchSeq atomic.Int64

// seedStatsBatches inserts n rows into stats_batches, the table that dominates
// telemetry on real panels, so there is something to delete. Batch IDs come
// from a package counter so repeated calls never collide on the primary key.
func seedStatsBatches(t *testing.T, st *Store, ctx context.Context, n int) {
	t.Helper()
	base := seededBatchSeq.Add(int64(n)) - int64(n)
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO stats_batches (node_id, batch_id, received_at) VALUES (?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := int64(0); i < int64(n); i++ {
		if _, err := stmt.ExecContext(ctx, 1, fmt.Sprintf("batch-%08d", base+i), ts(now())); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// dropStatsBatches deletes every seeded row, leaving its pages on the freelist.
func dropStatsBatches(t *testing.T, st *Store, ctx context.Context) {
	t.Helper()
	if _, err := st.DB.ExecContext(ctx, `DELETE FROM stats_batches`); err != nil {
		t.Fatal(err)
	}
}

func fileSizeAt(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// forceAutoVacuumOff converts a test database to the legacy mode where free
// pages can never be reclaimed. auto_vacuum is connection-local until VACUUM
// applies it, so both statements must share a connection.
func forceAutoVacuumOff(t *testing.T, st *Store, ctx context.Context) {
	t.Helper()
	conn, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = NONE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		t.Fatal(err)
	}
}

// footprintAt is the size of the database plus its WAL. A freshly written row
// may still live only in the WAL, so the main file alone understates it.
func footprintAt(t *testing.T, path string) int64 {
	t.Helper()
	return fileSizeAt(t, path) + fileSizeAt(t, path+"-wal")
}

func TestStorageStatsTracksFreePages(t *testing.T) {
	st, ctx := testStore(t)
	// A database created by Open must start in incremental auto-vacuum mode;
	// otherwise the incremental reclaim path can never run.
	fresh, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.AutoVacuum != autoVacuumIncremental {
		t.Fatalf("fresh database auto_vacuum = %d, want %d", fresh.AutoVacuum, autoVacuumIncremental)
	}
	if fresh.PageSize <= 0 || fresh.PageCount <= 0 {
		t.Fatalf("fresh stats = %+v", fresh)
	}

	seedStatsBatches(t, st, ctx, 4000)
	dropStatsBatches(t, st, ctx)

	stats, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FreelistPages == 0 {
		t.Fatal("expected free pages after deleting rows")
	}
	if stats.FreeBytes != stats.FreelistPages*stats.PageSize {
		t.Fatalf("free bytes = %d, pages*size = %d", stats.FreeBytes, stats.FreelistPages*stats.PageSize)
	}
	if stats.LiveBytes+stats.FreeBytes != stats.PageCount*stats.PageSize {
		t.Fatalf("live %d + free %d != page count %d * %d",
			stats.LiveBytes, stats.FreeBytes, stats.PageCount, stats.PageSize)
	}
	// The rows are gone but the pages they used are still part of the database.
	if got, want := footprintAt(t, st.Path), stats.PageCount*stats.PageSize; got < want {
		t.Fatalf("footprint %d smaller than page count %d * %d", got, stats.PageCount, stats.PageSize)
	}
}

func TestReclaimReleasesFreePages(t *testing.T) {
	st, ctx := testStore(t)
	seedStatsBatches(t, st, ctx, 4000)
	before := footprintAt(t, st.Path)
	dropStatsBatches(t, st, ctx)

	freeBefore, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if freeBefore.FreelistPages == 0 {
		t.Fatal("expected free pages before reclaim")
	}
	freed, err := st.Reclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if freed == 0 {
		t.Fatal("reclaim released no pages")
	}
	freeAfter, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if freeAfter.FreelistPages >= freeBefore.FreelistPages {
		t.Fatalf("free pages %d -> %d, want a decrease", freeBefore.FreelistPages, freeAfter.FreelistPages)
	}
	if after := fileSizeAt(t, st.Path); after >= before {
		t.Fatalf("file size %d -> %d, want a decrease", before, after)
	}
	if wal := fileSizeAt(t, st.Path+"-wal"); wal != 0 {
		t.Fatalf("wal size = %d after reclaim, want 0", wal)
	}
}

func TestReclaimNoopWithoutIncrementalMode(t *testing.T) {
	st, ctx := testStore(t)
	forceAutoVacuumOff(t, st, ctx)
	stats, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.AutoVacuum != autoVacuumOff {
		t.Fatalf("auto_vacuum = %d, want %d", stats.AutoVacuum, autoVacuumOff)
	}

	seedStatsBatches(t, st, ctx, 4000)
	dropStatsBatches(t, st, ctx)
	freeBefore, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if freed, err := st.Reclaim(ctx); err != nil {
		t.Fatal(err)
	} else if freed != 0 {
		t.Fatalf("reclaim freed %d pages on a non-incremental database", freed)
	}
	if freeAfter, err := st.StorageStats(ctx); err != nil {
		t.Fatal(err)
	} else if freeAfter.FreelistPages != freeBefore.FreelistPages {
		t.Fatalf("free pages %d -> %d, want no change", freeBefore.FreelistPages, freeAfter.FreelistPages)
	}
}

func TestMaintainCompactsMostlyFreeDatabase(t *testing.T) {
	st, ctx := testStore(t)
	// Force a database without incremental auto-vacuum, like an installation
	// created before this setting existed.
	forceAutoVacuumOff(t, st, ctx)
	seedStatsBatches(t, st, ctx, 4000)
	before := footprintAt(t, st.Path)
	dropStatsBatches(t, st, ctx)

	lowerCompactThreshold(t, 1<<20)

	result, err := st.Maintain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Vacuumed {
		t.Fatalf("maintain result = %+v, want a vacuum", result)
	}
	stats, err := st.StorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.AutoVacuum != autoVacuumIncremental {
		t.Fatalf("auto_vacuum after compact = %d, want %d", stats.AutoVacuum, autoVacuumIncremental)
	}
	if stats.FreelistPages != 0 {
		t.Fatalf("freelist after compact = %d, want 0", stats.FreelistPages)
	}
	if after := fileSizeAt(t, st.Path); after >= before {
		t.Fatalf("file size %d -> %d, want a decrease", before, after)
	}
	if wal := fileSizeAt(t, st.Path+"-wal"); wal != 0 {
		t.Fatalf("wal size = %d after compact, want 0", wal)
	}

	// Nothing left to do on the next pass.
	again, err := st.Maintain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Vacuumed || again.ReclaimedPages != 0 {
		t.Fatalf("second maintain result = %+v, want no work", again)
	}
}

func TestMaintainReclaimsIncrementally(t *testing.T) {
	st, ctx := testStore(t)
	lowerReclaimThreshold(t, 16)
	seedStatsBatches(t, st, ctx, 4000)
	before := footprintAt(t, st.Path)
	dropStatsBatches(t, st, ctx)

	result, err := st.Maintain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Vacuumed {
		t.Fatalf("maintain result = %+v, want an incremental pass", result)
	}
	if result.ReclaimedPages == 0 {
		t.Fatalf("maintain result = %+v, want reclaimed pages", result)
	}
	if after := fileSizeAt(t, st.Path); after >= before {
		t.Fatalf("file size %d -> %d, want a decrease", before, after)
	}
}

func TestMaintainLeavesSmallFreelistsAlone(t *testing.T) {
	st, ctx := testStore(t)
	lowerReclaimThreshold(t, 1<<20)

	seedStatsBatches(t, st, ctx, 10)
	dropStatsBatches(t, st, ctx)
	before := footprintAt(t, st.Path)

	result, err := st.Maintain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Vacuumed || result.ReclaimedPages != 0 {
		t.Fatalf("maintain result = %+v, want no work", result)
	}
	if after := footprintAt(t, st.Path); after != before {
		t.Fatalf("footprint %d -> %d, want no change", before, after)
	}
}

func TestTruncateWALReportsBusyReaders(t *testing.T) {
	st, ctx := testStore(t)
	seedStatsBatches(t, st, ctx, 100)

	// Pin a read snapshot on another connection, then write more frames: the
	// truncating checkpoint cannot complete until that reader moves on.
	tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM stats_batches`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	seedStatsBatches(t, st, ctx, 100)

	err = st.TruncateWAL(ctx)
	if err == nil {
		t.Fatal("expected a busy checkpoint error")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("error = %v, want a blocked checkpoint message", err)
	}
}

// lowerCompactThreshold makes a full rewrite worthwhile for small test
// databases and restores the production value afterwards.
func lowerCompactThreshold(t *testing.T, bytes int64) {
	t.Helper()
	old := maintainCompactFreeBytes
	maintainCompactFreeBytes = bytes
	t.Cleanup(func() { maintainCompactFreeBytes = old })
}

// lowerReclaimThreshold raises the incremental reclaim threshold so small test
// databases are skipped.
func lowerReclaimThreshold(t *testing.T, pages int64) {
	t.Helper()
	old := maintainReclaimPages
	maintainReclaimPages = pages
	t.Cleanup(func() { maintainReclaimPages = old })
}
