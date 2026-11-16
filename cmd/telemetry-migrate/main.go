// Command telemetry-migrate copies parsed logs from the panel's SQLite
// database into PostgreSQL before switching telemetry.driver to postgres.
//
// The import is resumable and idempotent: batch ids are derived from the
// SQLite row id bucket, so re-running the command does not insert duplicates.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/server"
	"github.com/axmishell/mikvacepanel/internal/store/pglogs"
)

func main() {
	configPath := flag.String("config", server.DefaultPanelConfigPath(), "path to panel.yaml")
	batchSize := flag.Int("batch", 1000, "rows per import batch")
	fromID := flag.Int64("from-id", 0, "resume after this SQLite log id")
	skipReceipts := flag.Bool("skip-receipts", false, "do not copy log_batches idempotency markers")
	dryRun := flag.Bool("dry-run", false, "count rows and test connections without writing")
	flag.Parse()

	if *batchSize < 1 || *batchSize > 10000 {
		log.Fatalf("--batch must be between 1 and 10000")
	}
	// Resume boundaries are id buckets, so round --from-id down to a bucket
	// start. This prevents a half-read bucket from being recorded under the
	// same synthetic batch id as a later, complete pass.
	*fromID = (*fromID / int64(*batchSize)) * int64(*batchSize)
	cfg, err := server.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load %s: %v", *configPath, err)
	}
	if cfg.TelemetryDriver != "postgres" {
		log.Fatalf("telemetry.driver is %q; set it to postgres before migrating", cfg.TelemetryDriver)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlite, err := openSQLite(cfg.DBPath)
	if err != nil {
		log.Fatalf("open control database: %v", err)
	}
	defer sqlite.Close()

	pg, err := pglogs.Open(ctx, pglogs.Config{
		DSN:             cfg.TelemetryDSN,
		Schema:          cfg.TelemetrySchema,
		MaxOpenConns:    cfg.TelemetryMaxOpen,
		MaxIdleConns:    cfg.TelemetryMaxIdle,
		ConnMaxLifetime: cfg.TelemetryConnMaxLife,
	})
	if err != nil {
		log.Fatalf("open postgres telemetry: %v", err)
	}
	defer pg.Close()

	total, err := countLogs(ctx, sqlite, *fromID)
	if err != nil {
		log.Fatalf("count logs: %v", err)
	}
	log.Printf("found %d log rows to migrate (from id %d)", total, *fromID)
	if *dryRun {
		log.Printf("dry run: connections are healthy, nothing written")
		return
	}

	rows, batches, err := migrateLogs(ctx, sqlite, pg, *fromID, *batchSize)
	if err != nil {
		log.Fatalf("migrate logs after %d rows: %v", rows, err)
	}
	log.Printf("migrated %d log rows in %d batches", rows, batches)

	if !*skipReceipts {
		n, err := migrateReceipts(ctx, sqlite, pg)
		if err != nil {
			log.Fatalf("migrate batch receipts after %d rows: %v", n, err)
		}
		log.Printf("copied %d batch receipts", n)
	}
	log.Printf("done")
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func countLogs(ctx context.Context, db *sql.DB, fromID int64) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hy2_logs WHERE id >= ?`, fromID).Scan(&n)
	return n, err
}

func migrateLogs(ctx context.Context, sqlite *sql.DB, pg *pglogs.Store, fromID int64, batchSize int) (int64, int, error) {
	rows, err := sqlite.QueryContext(ctx,
		`SELECT id, node_id, ts, level, logger, message, raw, event, client_ip, user_id,
		 host, port, bytes_tx, bytes_rx, duration_ms, error
		 FROM hy2_logs WHERE id >= ? ORDER BY id`, fromID)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var (
		total    int64
		batches  int
		pending  []model.LogEntry
		pendingN int64
		bucket   int64 = -1
		node     int64 = -1
	)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		batchID := fmt.Sprintf("migrate-%d-%d", bucket*int64(batchSize), node)
		if _, err := pg.ImportLogBatch(ctx, node, batchID, pending); err != nil {
			return err
		}
		total += int64(len(pending))
		batches++
		pending = pending[:0]
		pendingN = 0
		return nil
	}
	for rows.Next() {
		var (
			id        int64
			nodeID    int64
			ts        string
			e         model.LogEntry
			level     string
			logger    string
			message   string
			raw       string
			event     string
			clientIP  string
			userID    string
			host      string
			port      int
			bytesTx   int64
			bytesRx   int64
			duration  int64
			errString string
		)
		if err := rows.Scan(&id, &nodeID, &ts, &level, &logger, &message, &raw, &event,
			&clientIP, &userID, &host, &port, &bytesTx, &bytesRx, &duration, &errString); err != nil {
			return total, batches, err
		}
		e = model.LogEntry{
			ID: id, NodeID: nodeID, Level: level, Logger: logger, Message: message,
			Raw: raw, Event: event, ClientIP: clientIP, UserID: userID, Host: host,
			Port: port, BytesTx: bytesTx, BytesRx: bytesRx, DurationMS: duration, Error: errString,
		}
		e.TS, _ = time.Parse(time.RFC3339Nano, ts)
		nextBucket := id / int64(batchSize)
		if nextBucket != bucket || nodeID != node {
			if err := flush(); err != nil {
				return total, batches, err
			}
			bucket = nextBucket
			node = nodeID
		}
		pending = append(pending, e)
		pendingN++
		if pendingN%5000 == 0 {
			log.Printf("progress: %d rows", total+int64(len(pending)))
		}
	}
	if err := rows.Err(); err != nil {
		return total, batches, err
	}
	if err := flush(); err != nil {
		return total, batches, err
	}
	return total, batches, nil
}

func migrateReceipts(ctx context.Context, sqlite *sql.DB, pg *pglogs.Store) (int64, error) {
	rows, err := sqlite.QueryContext(ctx,
		`SELECT node_id, batch_id, received_at FROM log_batches ORDER BY node_id, batch_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var (
		total   int64
		pending []pglogs.BatchReceipt
	)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := pg.ImportBatchReceipts(ctx, pending); err != nil {
			return err
		}
		total += int64(len(pending))
		pending = pending[:0]
		return nil
	}
	for rows.Next() {
		var r pglogs.BatchReceipt
		var received string
		if err := rows.Scan(&r.NodeID, &r.BatchID, &received); err != nil {
			return total, err
		}
		r.ReceivedAt, _ = time.Parse(time.RFC3339Nano, received)
		pending = append(pending, r)
		if len(pending) >= 500 {
			if err := flush(); err != nil {
				return total, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return total, err
	}
	if err := flush(); err != nil {
		return total, err
	}
	return total, nil
}
