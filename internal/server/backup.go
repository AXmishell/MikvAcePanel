package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/celerity"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/version"
)

// BackupInfo describes one local backup archive.
type BackupInfo struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) backupDir() string { return filepath.Join(s.cfg.DataDir, "backups") }

// CreateBackup snapshots SQLite and writes a compressed archive. The database
// snapshot uses VACUUM INTO, so the resulting file is consistent even while
// the panel is serving requests.
func (s *Server) CreateBackup(ctx context.Context) (*BackupInfo, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	nowT := time.Now().UTC()
	name := "mikvace-" + nowT.Format("20060102-150405") + ".tar.gz"
	finalPath := filepath.Join(dir, name)
	tmpPath := finalPath + ".tmp"
	snapshot := filepath.Join(dir, ".snapshot-"+store.RandomToken(8)+".db")
	defer os.Remove(snapshot)
	if _, err := s.store.DB.ExecContext(ctx, `VACUUM INTO ?`, snapshot); err != nil {
		return nil, fmt.Errorf("snapshot database: %w", err)
	}
	dbFile, err := os.Open(snapshot)
	if err != nil {
		return nil, err
	}
	defer dbFile.Close()
	stat, err := dbFile.Stat()
	if err != nil {
		return nil, err
	}
	fw, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	gz := gzip.NewWriter(fw)
	tw := tar.NewWriter(gz)
	writeErr := tw.WriteHeader(&tar.Header{
		Name: "panel.db", Mode: 0o600, Size: stat.Size(), ModTime: nowT, Typeflag: tar.TypeReg,
	})
	if writeErr == nil {
		_, writeErr = io.Copy(tw, dbFile)
	}
	meta := map[string]any{
		"app": "MikvAce Panel", "version": version.String(),
		"createdAt": nowT.Format(time.RFC3339), "databaseSize": stat.Size(),
	}
	if writeErr == nil {
		var metaBytes []byte
		metaBytes, _ = json.MarshalIndent(meta, "", "  ")
		writeErr = tw.WriteHeader(&tar.Header{
			Name: "meta.json", Mode: 0o600, Size: int64(len(metaBytes)), ModTime: nowT, Typeflag: tar.TypeReg,
		})
		if writeErr == nil {
			_, writeErr = tw.Write(metaBytes)
		}
	}
	if err := tw.Close(); writeErr == nil {
		writeErr = err
	}
	if err := gz.Close(); writeErr == nil {
		writeErr = err
	}
	if err := fw.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return nil, writeErr
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	keep := parseIntSetting(settingOrContext(s, ctx, "backup_keep", "7"), 7)
	s.pruneBackups(keep)
	archiveStat, statErr := os.Stat(finalPath)
	if statErr != nil {
		return nil, statErr
	}
	return &BackupInfo{Name: name, Size: archiveStat.Size(), CreatedAt: nowT}, nil
}

func (s *Server) listBackups() ([]BackupInfo, error) {
	dir := s.backupDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, err
	}
	out := []BackupInfo{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{Name: entry.Name(), Size: info.Size(), CreatedAt: info.ModTime().UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Server) pruneBackups(keep int) {
	if keep <= 0 {
		keep = 7
	}
	list, err := s.listBackups()
	if err != nil || len(list) <= keep {
		return
	}
	for _, b := range list[keep:] {
		_ = os.Remove(filepath.Join(s.backupDir(), b.Name))
	}
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	list, err := s.listBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"items": list, "enabled": settings["backup_enabled"],
		"intervalHours": settings["backup_interval_hours"], "keep": settings["backup_keep"],
	})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	info, err := s.CreateBackup(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.AddEvent(r.Context(), "backup.created", 0, "info", "备份 "+info.Name+" 已创建")
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := routeParam(r, "name")
	path, ok := s.safeBackupPath(name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	http.ServeFile(w, r, path)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	name := routeParam(r, "name")
	path, ok := s.safeBackupPath(name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) safeBackupPath(name string) (string, bool) {
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".tar.gz") {
		return "", false
	}
	return filepath.Join(s.backupDir(), name), true
}

// handleRestoreBackup accepts a .tar.gz produced by CreateBackup. The database
// is staged next to the live file and swapped in on the next panel start,
// which avoids closing the SQLite connection underneath running requests.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing backup file field")
		return
	}
	defer file.Close()
	tmpTar := filepath.Join(s.cfg.DataDir, ".restore-"+store.RandomToken(8)+".tar.gz")
	defer os.Remove(tmpTar)
	tf, err := os.OpenFile(tmpTar, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, copyErr := io.Copy(tf, file)
	closeErr := tf.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusBadRequest, "could not store upload")
		return
	}
	staged := s.cfg.DBPath + ".restore"
	_ = os.Remove(staged)
	_ = os.Remove(staged + "-wal")
	_ = os.Remove(staged + "-shm")

	isCelerity, detectErr := celerity.IsBackup(tmpTar)
	if detectErr != nil {
		writeError(w, http.StatusBadRequest, "invalid backup archive: "+detectErr.Error())
		return
	}
	if isCelerity {
		var opts celerity.Options
		if c := claimsFrom(r); c != nil {
			if admin, err := s.store.GetAdminByID(r.Context(), c.AdminID); err == nil {
				opts.FallbackAdminUsername = admin.Username
				opts.FallbackAdminPasswordHash = admin.PasswordHash
			}
		}
		summary, err := celerity.BuildDatabase(r.Context(), tmpTar, staged, opts)
		if err != nil {
			_ = os.Remove(staged)
			_ = os.Remove(staged + "-wal")
			_ = os.Remove(staged + "-shm")
			writeError(w, http.StatusBadRequest, "invalid CELERITY backup: "+err.Error())
			return
		}
		if err := store.ValidateDatabase(staged); err != nil {
			_ = os.Remove(staged)
			writeError(w, http.StatusBadRequest, "invalid imported database: "+err.Error())
			return
		}
		_ = os.Chmod(staged, 0o600)
		s.store.AddEvent(r.Context(), "backup.restore_staged", 0, "warning",
			fmt.Sprintf("已导入 CELERITY 备份：%d 用户 / %d 节点 / %d 分组 / %d API 密钥，等待面板重启",
				summary.Users, summary.Nodes, summary.Groups, summary.APIKeys))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "restartRequired": true, "source": "celerity",
			"summary":  summary,
			"warnings": summary.Warnings,
			"message": fmt.Sprintf("CELERITY backup imported: %d users, %d nodes, %d groups, %d API keys. Restart the panel to apply it.",
				summary.Users, summary.Nodes, summary.Groups, summary.APIKeys),
		})
		return
	}
	if err := extractDatabase(tmpTar, staged); err != nil {
		_ = os.Remove(staged)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.ValidateDatabase(staged); err != nil {
		_ = os.Remove(staged)
		writeError(w, http.StatusBadRequest, "invalid backup database: "+err.Error())
		return
	}
	s.store.AddEvent(r.Context(), "backup.restore_staged", 0, "warning", "已上传恢复文件，等待面板重启")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "restartRequired": true,
		"message": "Backup validated. Restart the panel to replace the current database.",
	})
}

func extractDatabase(tarPath, dest string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || (hdr.Name != "panel.db" && !strings.HasSuffix(hdr.Name, "/panel.db")) {
			continue
		}
		if hdr.Size > 2<<30 {
			return fmt.Errorf("database in archive is too large")
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, tr, hdr.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	}
	return fmt.Errorf("archive does not contain panel.db")
}

// MaybeAutoBackup creates a backup when the configured interval has elapsed.
func (s *Server) MaybeAutoBackup(ctx context.Context) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil || !parseBoolSetting(settings["backup_enabled"], true) {
		return
	}
	interval := parseIntSetting(settings["backup_interval_hours"], 24)
	if interval <= 0 {
		interval = 24
	}
	list, err := s.listBackups()
	if err == nil && len(list) > 0 {
		if time.Since(list[0].CreatedAt) < time.Duration(interval)*time.Hour {
			return
		}
	}
	if _, err := s.CreateBackup(ctx); err != nil {
		s.log.Warn("automatic backup failed", "error", err)
		return
	}
	list, _ = s.listBackups()
	if len(list) > 0 {
		s.store.AddEvent(ctx, "backup.auto", 0, "info", "自动备份 "+list[0].Name+" 已创建")
	}
}

var _ = sql.ErrNoRows
