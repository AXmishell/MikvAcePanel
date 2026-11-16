package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/celerity"
	"github.com/axmishell/mikvacepanel/internal/configfile"
	"github.com/axmishell/mikvacepanel/internal/idgen"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// maxInstallImportBytes mirrors the administrator restore upload limit. The
// installer is public until the first administrator exists, so the size cap and
// the login limiter are the only backstops before the archive is parsed.
const maxInstallImportBytes = 512 << 20

// installImportSummary is the subset of an imported database the wizard shows
// before and after the restart.
type installImportSummary struct {
	Users        int      `json:"users"`
	Groups       int      `json:"groups"`
	Nodes        int      `json:"nodes"`
	Admins       int      `json:"admins"`
	APIKeys      int      `json:"apiKeys"`
	SkippedUsers int      `json:"skippedUsers,omitempty"`
	SkippedNodes int      `json:"skippedNodes,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

type installImportResult struct {
	Source     string
	Summary    installImportSummary
	AdminEntry string
}

// handleInstallImport restores a panel backup during the first-run wizard.
// It accepts the same archives as /api/backups/restore: a MikvAce .tar.gz
// (panel.db + meta.json + optional cert.key) or a CELERITY mongodump. The
// database is staged as <path>.restore and applied by store.OpenConfig on the
// next boot, so the wizard never swaps a live SQLite file underneath requests.
func (s *Server) handleInstallImport(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "install-import", ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	s.installMu.Lock()
	defer s.installMu.Unlock()

	if s.store != nil {
		if n, err := s.store.CountAdmins(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not check installation state")
			return
		} else if n > 0 {
			writeError(w, http.StatusConflict, "panel is already installed")
			return
		}
	}
	if !configfile.Writable(s.cfg.ConfigPath) {
		writeError(w, http.StatusConflict, "config file is not writable: "+s.cfg.ConfigPath)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxInstallImportBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.recordLoginFailure(r.Context(), "install-import", ip)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "backup file is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.recordLoginFailure(r.Context(), "install-import", ip)
		writeError(w, http.StatusBadRequest, "missing backup file field")
		return
	}
	defer file.Close()

	target := strings.TrimSpace(r.FormValue("path"))
	if target == "" {
		target = strings.TrimSpace(s.cfg.DBPath)
	}
	if target == "" {
		target = filepath.Join(s.cfg.DataDir, "panel.db")
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		s.recordLoginFailure(r.Context(), "install-import", ip)
		writeError(w, http.StatusBadRequest, "backup target path is a directory")
		return
	}

	if err := os.MkdirAll(s.cfg.DataDir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create data directory: "+err.Error())
		return
	}
	tmp, err := os.CreateTemp(s.cfg.DataDir, ".install-import-*.tar.gz")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	copyErr := func() error {
		if _, err := io.Copy(tmp, file); err != nil {
			return err
		}
		return tmp.Close()
	}()
	if copyErr != nil {
		_ = tmp.Close()
		s.recordLoginFailure(r.Context(), "install-import", ip)
		writeError(w, http.StatusBadRequest, "could not store upload: "+copyErr.Error())
		return
	}

	// Parsing a CELERITY mongodump can take a while for large installations.
	// Keep the work alive even if the browser disconnects mid-upload.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	staged := target + ".restore"
	result, err := s.stageInstallImport(ctx, tmpPath, staged)
	if err != nil {
		s.discardStagedImport(staged)
		s.recordLoginFailure(r.Context(), "install-import", ip)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// An imported database may carry no usable security entrance (CELERITY
	// conversions and very old backups). Generate one now so the success page
	// can link straight to the login form; the next boot persists the same
	// value to data_dir/admin-entry.txt.
	if result.Summary.Admins == 0 {
		result.AdminEntry = ""
	} else if !validAdminSecurityPath(strings.TrimSpace(result.AdminEntry)) {
		entry := idgen.Token(8)
		if err := store.SetDatabaseSetting(staged, "admin_security_path", entry); err != nil {
			s.discardStagedImport(staged)
			writeError(w, http.StatusInternalServerError, "could not prepare security entrance: "+err.Error())
			return
		}
		if err := store.SetDatabaseSetting(staged, "admin_security_enabled", "true"); err != nil {
			s.log.Warn("install import: enable security entrance", "error", err)
		}
		result.AdminEntry = entry
	}

	if err := configfile.Set(s.cfg.ConfigPath, map[string]any{
		"database.driver": "sqlite",
		"database.path":   target,
	}); err != nil {
		s.discardStagedImport(staged)
		writeError(w, http.StatusInternalServerError,
			"could not write config file "+s.cfg.ConfigPath+": "+err.Error())
		return
	}

	if s.store != nil {
		s.store.AddEvent(r.Context(), "backup.import_staged", 0, "warning",
			fmt.Sprintf("安装向导已导入 %s 备份：%d 用户 / %d 节点 / %d 分组 / %d 管理员，等待面板重启",
				result.Source, result.Summary.Users, result.Summary.Nodes,
				result.Summary.Groups, result.Summary.Admins))
	}
	if s.cfg.Restart != nil {
		s.log.Warn("backup imported during installation; restarting panel",
			"source", result.Source, "path", target, "admins", result.Summary.Admins)
		// Give net/http a moment to flush the JSON response before exec.
		time.AfterFunc(600*time.Millisecond, s.cfg.Restart)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"restartRequired": true,
		"source":          result.Source,
		"path":            target,
		"summary":         result.Summary,
		"warnings":        result.Summary.Warnings,
		"hasAdmin":        result.Summary.Admins > 0,
		"adminEntry":      result.AdminEntry,
		"message":         installImportMessage(result),
	})
}

// stageInstallImport validates the archive and writes a staged database. The
// caller owns the staged file and must remove it when a later step fails.
func (s *Server) stageInstallImport(ctx context.Context, archivePath, staged string) (installImportResult, error) {
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		return installImportResult{}, fmt.Errorf("create database directory: %w", err)
	}
	removeStagedDatabase(staged)

	isCelerity, err := celerity.IsBackup(archivePath)
	if err != nil {
		return installImportResult{}, fmt.Errorf("invalid backup archive: %w", err)
	}
	var result installImportResult
	if isCelerity {
		summary, err := celerity.BuildDatabase(ctx, archivePath, staged, celerity.Options{})
		if err != nil {
			return installImportResult{}, fmt.Errorf("invalid CELERITY backup: %w", err)
		}
		if err := store.ValidateDatabase(staged); err != nil {
			return installImportResult{}, fmt.Errorf("invalid imported database: %w", err)
		}
		result.Source = "celerity"
		result.Summary = installImportSummary{
			Users: summary.Users, Groups: summary.Groups, Nodes: summary.Nodes,
			Admins: summary.Admins, APIKeys: summary.APIKeys,
			SkippedUsers: summary.SkippedUsers, SkippedNodes: summary.SkippedNodes,
			Warnings: summary.Warnings,
		}
	} else {
		if err := extractDatabase(archivePath, staged); err != nil {
			return installImportResult{}, fmt.Errorf("invalid backup archive: %w", err)
		}
		if err := store.ValidateDatabase(staged); err != nil {
			return installImportResult{}, fmt.Errorf("invalid backup database: %w", err)
		}
		info, err := store.InspectDatabase(staged)
		if err != nil {
			return installImportResult{}, fmt.Errorf("could not inspect backup: %w", err)
		}
		result.Source = "mikvace"
		result.Summary = installImportSummary{
			Users: info.Users, Groups: info.Groups, Nodes: info.Nodes,
			Admins: info.Admins, APIKeys: info.APIKeys,
		}
		result.AdminEntry = info.AdminSecurityPath
		// The matching encryption key keeps certificate private keys and other
		// encrypted secrets readable after the restore.
		keyDest := filepath.Join(s.cfg.DataDir, "cert.key.restore")
		if err := extractNamedFile(archivePath, "cert.key", keyDest, 0o600); err != nil {
			_ = os.Remove(keyDest)
		}
	}
	if err := os.Chmod(staged, 0o600); err != nil {
		return installImportResult{}, err
	}
	return result, nil
}

func removeStagedDatabase(staged string) {
	_ = os.Remove(staged)
	_ = os.Remove(staged + "-wal")
	_ = os.Remove(staged + "-shm")
}

// discardStagedImport also drops a staged encryption key so a failed import
// cannot make the next boot adopt the backup's key while keeping the live
// database.
func (s *Server) discardStagedImport(staged string) {
	removeStagedDatabase(staged)
	_ = os.Remove(filepath.Join(s.cfg.DataDir, "cert.key.restore"))
}

func installImportMessage(result installImportResult) string {
	if result.Summary.Admins == 0 {
		return fmt.Sprintf("备份已导入：%d 用户 / %d 节点 / %d 分组。重启后请继续创建管理员。",
			result.Summary.Users, result.Summary.Nodes, result.Summary.Groups)
	}
	return fmt.Sprintf("备份已导入：%d 用户 / %d 节点 / %d 分组 / %d 管理员。重启后请使用备份中的管理员账号登录。",
		result.Summary.Users, result.Summary.Nodes, result.Summary.Groups, result.Summary.Admins)
}
