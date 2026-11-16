package store

import (
	"context"
	"errors"
	"fmt"
)

// SQLite auto_vacuum modes. Incremental keeps a freelist that
// PRAGMA incremental_vacuum can hand back to the filesystem without rewriting
// the whole database.
const (
	autoVacuumOff         = 0
	autoVacuumIncremental = 2
)

// Maintenance thresholds. They are variables so tests can lower them without
// having to fill a database with tens of megabytes of throwaway rows.
var (
	// maintainCompactFreeBytes is the amount of free space that makes a full
	// rewrite (VACUUM) worthwhile.
	maintainCompactFreeBytes int64 = 64 << 20
	// maintainCompactFreeRatio is the share of the file that must be free for a
	// rewrite, so small databases are not vacuumed over a few spare pages.
	maintainCompactFreeRatio = 0.25
	// maintainReclaimPages is the free-page count that justifies an incremental
	// pass (256 pages = 1 MiB with the default page size).
	maintainReclaimPages int64 = 256
)

// StorageStats splits the SQLite file into live data and free pages. Deleted
// rows leave their pages on a freelist: the rows disappear, but the file only
// shrinks after VACUUM or PRAGMA incremental_vacuum.
type StorageStats struct {
	PageSize      int64 `json:"pageSize"`
	PageCount     int64 `json:"pageCount"`
	FreelistPages int64 `json:"freelistPages"`
	// LiveBytes is the size of the pages that still hold data.
	LiveBytes int64 `json:"liveBytes"`
	// FreeBytes is space already reclaimed from tables but not from the file.
	FreeBytes int64 `json:"freeBytes"`
	// AutoVacuum is 0 when disabled, 2 when incremental reclamation is enabled.
	AutoVacuum int `json:"autoVacuum"`
}

// MaintainResult reports what an automatic maintenance pass did.
type MaintainResult struct {
	// Vacuumed is true when the database was rewritten and compacted.
	Vacuumed bool `json:"vacuumed"`
	// ReclaimedPages counts pages released by an incremental vacuum pass.
	ReclaimedPages int64 `json:"reclaimedPages"`
	// FreeBytes is the free space left on the freelist afterwards.
	FreeBytes int64 `json:"freeBytes"`
}

// StorageStats reads the SQLite page counters.
func (s *Store) StorageStats(ctx context.Context) (StorageStats, error) {
	var st StorageStats
	var err error
	read := func(sql string, dst any) {
		if err != nil {
			return
		}
		err = s.DB.QueryRowContext(ctx, sql).Scan(dst)
	}
	read(`PRAGMA page_size`, &st.PageSize)
	read(`PRAGMA page_count`, &st.PageCount)
	read(`PRAGMA freelist_count`, &st.FreelistPages)
	read(`PRAGMA auto_vacuum`, &st.AutoVacuum)
	if err != nil {
		return StorageStats{}, err
	}
	if st.PageSize < 0 || st.PageCount < 0 || st.FreelistPages < 0 {
		return StorageStats{}, fmt.Errorf("unexpected sqlite page counters: %+v", st)
	}
	if st.FreelistPages > st.PageCount {
		st.FreelistPages = st.PageCount
	}
	st.LiveBytes = (st.PageCount - st.FreelistPages) * st.PageSize
	st.FreeBytes = st.FreelistPages * st.PageSize
	return st, nil
}

// errWALBusy reports that a reader prevented the WAL from being truncated.
var errWALBusy = errors.New("wal checkpoint blocked by an active reader; wal was not truncated")

// walBusyTimeout bounds how long a truncating checkpoint waits for locks. The
// pool default (8s) is meant for queries, but maintenance should report a
// blocked checkpoint instead of stalling the request for that long.
const walBusyTimeout = 500

// TruncateWAL checkpoints every WAL frame into the main file and shrinks the
// -wal file to zero bytes. SQLite reports a blocked checkpoint in a result row
// instead of returning an error, so the busy flag is checked explicitly.
func (s *Store) TruncateWAL(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var poolTimeout int
	if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&poolTimeout); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA busy_timeout = %d`, walBusyTimeout)); err != nil {
		return err
	}
	// Put the pooled connection back the way the DSN left it.
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx),
			fmt.Sprintf(`PRAGMA busy_timeout = %d`, poolTimeout))
	}()

	var busy, logPages, checkpointed int
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &logPages, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return errWALBusy
	}
	return nil
}

// Compact rewrites the database, reclaiming the space left behind by pruned
// rows, and switches it to incremental auto-vacuum so later passes are cheap.
// It can briefly block other writers.
func (s *Store) Compact(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// auto_vacuum is a connection-local setting until the following VACUUM
	// writes it to the file header, so both statements must share a connection.
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	// The rewrite wrote its pages through the WAL, so the checkpoint has to run
	// afterwards. Checkpointing before VACUUM left the WAL at its high-water
	// mark.
	return s.TruncateWAL(ctx)
}

// Reclaim runs an incremental vacuum pass, which releases free pages from the
// end of the file without rewriting live tables. Databases that still have
// auto_vacuum disabled are left untouched; Compact converts them.
func (s *Store) Reclaim(ctx context.Context) (int64, error) {
	before, err := s.StorageStats(ctx)
	if err != nil {
		return 0, err
	}
	if before.AutoVacuum != autoVacuumIncremental || before.FreelistPages == 0 {
		return 0, nil
	}
	if _, err := s.DB.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		return 0, err
	}
	after, err := s.StorageStats(ctx)
	if err != nil {
		return 0, err
	}
	freed := before.FreelistPages - after.FreelistPages
	if freed < 0 {
		freed = 0
	}
	if err := s.TruncateWAL(ctx); err != nil {
		return freed, err
	}
	return freed, nil
}

// Maintain reclaims space left by pruning without an operator having to tick a
// box: it rewrites the file when most of it is holes, and otherwise performs a
// cheap incremental pass.
func (s *Store) Maintain(ctx context.Context) (MaintainResult, error) {
	var res MaintainResult
	stats, err := s.StorageStats(ctx)
	if err != nil {
		return res, err
	}
	res.FreeBytes = stats.FreeBytes
	if stats.FreelistPages == 0 {
		return res, nil
	}
	freeRatio := 0.0
	if stats.PageCount > 0 {
		freeRatio = float64(stats.FreelistPages) / float64(stats.PageCount)
	}
	// A database that still has auto-vacuum disabled cannot reclaim anything
	// incrementally, so a rewrite that also enables incremental mode is the
	// only way to stop the bloat from coming back.
	worthRewrite := stats.FreeBytes >= maintainCompactFreeBytes || freeRatio >= maintainCompactFreeRatio
	if stats.AutoVacuum != autoVacuumIncremental && worthRewrite {
		if err := s.Compact(ctx); err != nil {
			return res, err
		}
		res.Vacuumed = true
		if after, err := s.StorageStats(ctx); err == nil {
			res.FreeBytes = after.FreeBytes
		}
		return res, nil
	}
	if stats.FreelistPages < maintainReclaimPages {
		return res, nil
	}
	if stats.AutoVacuum != autoVacuumIncremental {
		return res, nil
	}
	freed, err := s.Reclaim(ctx)
	res.ReclaimedPages = freed
	if err != nil {
		return res, err
	}
	if after, err := s.StorageStats(ctx); err == nil {
		res.FreeBytes = after.FreeBytes
	}
	return res, nil
}

// EnableIncrementalAutoVacuum configures a fresh database so that it can
// release free pages later without a full rewrite. Setting the pragma on its
// own is not enough (the value lives in the file header and is applied by
// VACUUM), so both statements run on one connection. Databases that already
// have tables are left untouched; Compact converts those.
func (s *Store) EnableIncrementalAutoVacuum(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var tables int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 {
		return nil
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `VACUUM`)
	return err
}
