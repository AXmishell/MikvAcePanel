package server

import "github.com/go-chi/chi/v5"

// registerAdminRoutes contains the authenticated administrator API. Keeping the
// route table next to the handlers makes ownership obvious and lets new
// modules add their own registration function instead of growing one router.
func (s *Server) registerAdminRoutes(r chi.Router) {
	r.Group(func(pr chi.Router) {
		pr.Use(s.adminAuth)

		// Account and 2FA.
		pr.Get("/api/me", s.handleMe)
		pr.Post("/api/logout", s.handleLogout)
		pr.Put("/api/admin/account", s.handleUpdateAccount)
		pr.Post("/api/change-password", s.handleChangePassword)
		pr.Get("/api/2fa", s.handleTwoFactorStatus)
		pr.Post("/api/2fa/setup", s.handleTwoFactorSetup)
		pr.Post("/api/2fa/enable", s.handleTwoFactorEnable)
		pr.Post("/api/2fa/disable", s.handleTwoFactorDisable)
		pr.Post("/api/2fa/recovery", s.handleTwoFactorRecovery)

		// Dashboard and global sync.
		pr.Get("/api/dashboard", s.handleDashboard)
		pr.Get("/api/events", s.handleEvents)
		pr.Post("/api/sync", s.handleSyncAll)

		// Users.
		pr.Get("/api/users", s.handleListUsers)
		pr.Post("/api/users", s.handleCreateUser)
		pr.Get("/api/users/{id}", s.handleGetUser)
		pr.Get("/api/users/{id}/connections", s.handleUserConnections)
		pr.Put("/api/users/{id}", s.handleUpdateUser)
		pr.Delete("/api/users/{id}", s.handleDeleteUser)
		pr.Post("/api/users/{id}/enable", s.handleEnableUser)
		pr.Post("/api/users/{id}/disable", s.handleDisableUser)
		pr.Post("/api/users/{id}/reset-subscription", s.handleResetUserSubscription)
		pr.Post("/api/users/{id}/reset-password", s.handleResetUserPassword)
		pr.Post("/api/users/{id}/reset-devices", s.handleResetDevices)
		pr.Post("/api/users/{id}/reset-traffic", s.handleResetUserTraffic)

		// Groups.
		pr.Get("/api/groups", s.handleListGroups)
		pr.Post("/api/groups", s.handleCreateGroup)
		pr.Put("/api/groups/{id}", s.handleUpdateGroup)
		pr.Delete("/api/groups/{id}", s.handleDeleteGroup)

		// Nodes and certificates.
		pr.Get("/api/nodes", s.handleListNodes)
		pr.Post("/api/nodes", s.handleCreateNode)
		pr.Post("/api/nodes/reorder", s.handleReorderNodes)
		pr.Get("/api/nodes/{id}", s.handleGetNode)
		pr.Put("/api/nodes/{id}", s.handleUpdateNode)
		pr.Delete("/api/nodes/{id}", s.handleDeleteNode)
		pr.Post("/api/nodes/{id}/token", s.handleResetNodeToken)
		pr.Post("/api/nodes/{id}/sync", s.handleSyncNode)
		pr.Post("/api/nodes/{id}/command", s.handleNodeCommand)
		pr.Post("/api/nodes/{id}/ssh/install", s.handleNodeSSHInstall)
		pr.Get("/api/nodes/{id}/config", s.handleNodeConfig)
		pr.Get("/api/nodes/{id}/install", s.handleNodeInstall)
		pr.Get("/api/nodes/{id}/cert", s.handleGetNodeCert)
		pr.Post("/api/nodes/{id}/cert/request", s.handleRequestNodeCert)
		pr.Post("/api/nodes/{id}/cert/upload", s.handleUploadNodeCert)
		pr.Post("/api/nodes/{id}/cert/confirm", s.handleConfirmNodeCert)
		pr.Post("/api/nodes/{id}/cert/cancel", s.handleCancelNodeCert)
		pr.Get("/api/certs", s.handleListNodeCerts)
		pr.Get("/api/cert/settings", s.handleGetCertSettings)
		pr.Put("/api/cert/settings", s.handleUpdateCertSettings)

		// Logs.
		pr.Get("/api/logs", s.handleLogs)
		pr.Get("/api/logs/export", s.handleExportLogs)
		pr.Get("/api/logs/summary", s.handleLogSummary)
		pr.Get("/api/logs/options", s.handleLogOptions)
		pr.Delete("/api/logs", s.handleDeleteLogs)

		// Maintenance and telemetry cleanup.
		pr.Get("/api/maintenance/usage", s.handleDataUsage)
		pr.Post("/api/maintenance/cleanup", s.handleCleanupData)

		// Optional infrastructure: PostgreSQL log storage and Redis cache.
		// These endpoints are administrator-session only; API keys must not be
		// able to point the panel at another database.
		pr.With(s.requireAdminSession).Get("/api/infra", s.handleGetInfra)
		pr.With(s.requireAdminSession).Post("/api/infra/test", s.handleTestInfra)
		pr.With(s.requireAdminSession).Put("/api/infra", s.handleSaveInfra)

		// Settings, backups and API keys.
		pr.Get("/api/settings", s.handleGetSettings)
		pr.Put("/api/settings", s.handleUpdateSettings)
		pr.Get("/api/backups", s.handleListBackups)
		pr.Post("/api/backups", s.handleCreateBackup)
		pr.Get("/api/backups/{name}", s.handleDownloadBackup)
		pr.Delete("/api/backups/{name}", s.handleDeleteBackup)
		pr.Post("/api/backups/restore", s.handleRestoreBackup)
		pr.Get("/api/api-keys", s.handleListAPIKeys)
		pr.Post("/api/api-keys", s.handleCreateAPIKey)
		pr.Put("/api/api-keys/{id}", s.handleUpdateAPIKey)
		pr.Post("/api/api-keys/{id}/rotate", s.handleRotateAPIKey)
		pr.Delete("/api/api-keys/{id}", s.handleDeleteAPIKey)
		pr.Post("/api/api-keys/{id}/enable", s.handleEnableAPIKey)
		pr.Post("/api/api-keys/{id}/disable", s.handleDisableAPIKey)
	})
}
