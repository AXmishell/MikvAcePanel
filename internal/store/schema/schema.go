// Package schema is the single source of truth for the panel database schema.
//
// DDL is generated per dialect from these definitions; migrations compare the
// live database against the same metadata. Keep column names, defaults and
// indexes in sync with the SQL used by the store package.
package schema

// Kind is the portable column type.
type Kind int

const (
	Int Kind = iota
	Int64
	Bool
	Float
	String
	Text
	Time
	JSON
)

// Column describes one table column.
type Column struct {
	Name    string
	Kind    Kind
	Size    int // VARCHAR size for String; 0 means TEXT
	NotNull bool
	// Default is nil when the column has no default. Strings, integers,
	// booleans and floats are rendered as SQL literals by the dialect.
	Default any
	PK      bool
	Auto    bool
	Unique  bool
}

// ForeignKey describes a table-level foreign key.
type ForeignKey struct {
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   string
}

// Index describes a secondary index.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
}

// Table describes one table.
type Table struct {
	Name        string
	Columns     []Column
	PrimaryKey  []string
	ForeignKeys []ForeignKey
	Indexes     []Index
}

// MigrationTable tracks applied schema/data migrations.
var MigrationTable = Table{
	Name: "schema_migrations",
	Columns: []Column{
		{Name: "version", Kind: Int64, NotNull: true, PK: true},
		{Name: "applied_at", Kind: Time, NotNull: true},
	},
}

// Tables is ordered so referenced tables are created first.
var Tables = []Table{
	adminsTable(),
	groupsTable(),
	usersTable(),
	nodesTable(),
	userGroupsTable(),
	userNodesTable(),
	nodeGroupsTable(),
	nodeConfigsTable(),
	certificatesTable(),
	nodeCertificatesTable(),
	acmeAccountsTable(),
	nodeMetricsTable(),
	eventsTable(),
	logsTable(),
	logBatchesTable(),
	statsBatchesTable(),
	settingsTable(),
	apiKeysTable(),
}

func id() Column { return Column{Name: "id", Kind: Int64, PK: true, Auto: true} }

func str(name string, size int) Column {
	return Column{Name: name, Kind: String, Size: size, NotNull: true}
}

func strDef(name string, size int, def string) Column {
	c := str(name, size)
	c.Default = def
	return c
}

func text(name string) Column {
	return Column{Name: name, Kind: Text, NotNull: true}
}

func textDef(name, def string) Column {
	c := text(name)
	c.Default = def
	return c
}

func optText(name string) Column {
	return Column{Name: name, Kind: Text}
}

func timeCol(name string) Column {
	return Column{Name: name, Kind: Time, NotNull: true}
}

func optTime(name string) Column {
	return Column{Name: name, Kind: Time}
}

func integer(name string, def any) Column {
	return Column{Name: name, Kind: Int, NotNull: true, Default: def}
}

func bigint(name string, def any) Column {
	return Column{Name: name, Kind: Int64, NotNull: true, Default: def}
}

func boolean(name string, def bool) Column {
	return Column{Name: name, Kind: Bool, NotNull: true, Default: def}
}

func real(name string, def float64) Column {
	return Column{Name: name, Kind: Float, NotNull: true, Default: def}
}

func jsonDef(name, def string) Column {
	return Column{Name: name, Kind: JSON, NotNull: true, Default: def}
}

func unique(c Column) Column {
	c.Unique = true
	return c
}

func adminsTable() Table {
	return Table{
		Name: "admins",
		Columns: []Column{
			id(),
			unique(str("username", 191)),
			str("password_hash", 255),
			timeCol("created_at"),
			optTime("last_login_at"),
			strDef("totp_secret", 255, ""),
			strDef("totp_pending", 255, ""),
			boolean("totp_enabled", false),
			strDef("totp_recovery", 1024, "[]"),
			strDef("totp_pending_recovery", 1024, "[]"),
			integer("token_version", 0),
		},
	}
}

func groupsTable() Table {
	return Table{
		Name: "panel_groups",
		Columns: []Column{
			id(),
			unique(str("name", 191)),
			strDef("description", 512, ""),
			strDef("color", 32, "#38bdf8"),
			strDef("subscription_title", 191, ""),
			integer("max_devices", 0),
			timeCol("created_at"),
			timeCol("updated_at"),
		},
	}
}

func usersTable() Table {
	return Table{
		Name: "users",
		Columns: []Column{
			id(),
			unique(str("user_id", 191)),
			strDef("username", 191, ""),
			str("password", 255),
			strDef("login_password_hash", 255, ""),
			unique(str("subscription_token", 191)),
			strDef("xray_uuid", 64, ""),
			boolean("enabled", false),
			bigint("traffic_tx", 0),
			bigint("traffic_rx", 0),
			bigint("traffic_limit", 0),
			integer("max_devices", 0),
			optTime("expire_at"),
			optTime("last_seen_at"),
			timeCol("created_at"),
			timeCol("updated_at"),
		},
		Indexes: []Index{
			{Name: "idx_users_token", Columns: []string{"subscription_token"}},
			{Name: "idx_users_user_id", Columns: []string{"user_id"}},
		},
	}
}

func nodesTable() Table {
	return Table{
		Name: "nodes",
		Columns: []Column{
			id(),
			str("name", 191),
			strDef("type", 32, "hysteria"),
			strDef("flag", 16, ""),
			strDef("ip", 64, ""),
			strDef("domain", 255, ""),
			strDef("sni", 255, ""),
			strDef("listen", 255, ""),
			integer("port", 443),
			strDef("port_range", 255, ""),
			strDef("hop_interval", 64, ""),
			strDef("obfs_type", 32, ""),
			strDef("obfs_password", 255, ""),
			integer("stats_port", 9999),
			strDef("stats_secret", 191, ""),
			jsonDef("xray_json", "{}"),
			jsonDef("hy2_json", "{}"),
			integer("ssh_port", 22),
			strDef("ssh_user", 64, ""),
			strDef("ssh_password", 255, ""),
			textDef("ssh_key", ""),
			textDef("ssh_host_key", ""),
			boolean("ssh_install_docker", false),
			strDef("tls_mode", 32, "selfsigned"),
			strDef("acme_email", 255, ""),
			strDef("bandwidth_up", 64, ""),
			strDef("bandwidth_down", 64, ""),
			strDef("masquerade_url", 512, ""),
			jsonDef("acl_json", "[]"),
			jsonDef("outbounds_json", "[]"),
			integer("max_online", 0),
			real("ranking", 1),
			boolean("active", true),
			strDef("status", 32, "offline"),
			unique(str("token_hash", 64)),
			strDef("token", 191, ""),
			strDef("token_prefix", 32, ""),
			strDef("agent_version", 64, ""),
			strDef("os", 64, ""),
			strDef("arch", 32, ""),
			strDef("hy2_version", 64, ""),
			strDef("xray_version", 64, ""),
			jsonDef("cores_json", "[]"),
			strDef("runtime_mode", 32, "process"),
			strDef("runtime", 32, ""),
			optTime("last_seen_at"),
			bigint("config_revision", 0),
			bigint("applied_revision", 0),
			textDef("config_error", ""),
			bigint("traffic_tx", 0),
			bigint("traffic_rx", 0),
			integer("online_users", 0),
			timeCol("created_at"),
			timeCol("updated_at"),
			jsonDef("capabilities_json", "[]"),
			strDef("cert_fingerprint", 191, ""),
			optTime("cert_not_after"),
		},
	}
}

func userGroupsTable() Table {
	return Table{
		Name: "user_groups",
		Columns: []Column{
			{Name: "user_id", Kind: Int64, NotNull: true},
			{Name: "group_id", Kind: Int64, NotNull: true},
		},
		PrimaryKey: []string{"user_id", "group_id"},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			{Columns: []string{"group_id"}, RefTable: "panel_groups", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Indexes: []Index{
			{Name: "idx_user_groups_group", Columns: []string{"group_id"}},
		},
	}
}

func userNodesTable() Table {
	return Table{
		Name: "user_nodes",
		Columns: []Column{
			{Name: "user_id", Kind: Int64, NotNull: true},
			{Name: "node_id", Kind: Int64, NotNull: true},
		},
		PrimaryKey: []string{"user_id", "node_id"},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			{Columns: []string{"node_id"}, RefTable: "nodes", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Indexes: []Index{
			{Name: "idx_user_nodes_node", Columns: []string{"node_id"}},
		},
	}
}

func nodeGroupsTable() Table {
	return Table{
		Name: "node_groups",
		Columns: []Column{
			{Name: "node_id", Kind: Int64, NotNull: true},
			{Name: "group_id", Kind: Int64, NotNull: true},
		},
		PrimaryKey: []string{"node_id", "group_id"},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"node_id"}, RefTable: "nodes", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			{Columns: []string{"group_id"}, RefTable: "panel_groups", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Indexes: []Index{
			{Name: "idx_node_groups_group", Columns: []string{"group_id"}},
		},
	}
}

func nodeConfigsTable() Table {
	return Table{
		Name: "node_configs",
		Columns: []Column{
			id(),
			{Name: "node_id", Kind: Int64, NotNull: true},
			bigint("revision", 0),
			text("yaml"),
			str("hash", 64),
			textDef("configs_json", ""),
			timeCol("created_at"),
			optTime("applied_at"),
			textDef("last_error", ""),
		},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"node_id"}, RefTable: "nodes", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Indexes: []Index{
			{Name: "idx_node_configs_node", Columns: []string{"node_id", "revision"}},
			{Name: "idx_node_configs_unique", Columns: []string{"node_id", "revision"}, Unique: true},
		},
	}
}

func certificatesTable() Table {
	return Table{
		Name: "certificates",
		Columns: []Column{
			id(),
			{Name: "domains_json", Kind: JSON, NotNull: true},
			strDef("common_name", 255, ""),
			text("cert_pem"),
			textDef("key_cipher", ""),
			textDef("key_nonce", ""),
			unique(str("fingerprint", 128)),
			strDef("key_fingerprint", 191, ""),
			strDef("issuer", 255, ""),
			timeCol("not_before"),
			timeCol("not_after"),
			strDef("source", 32, "acme"),
			strDef("status", 32, "valid"),
			boolean("auto_renew", true),
			textDef("last_error", ""),
			timeCol("created_at"),
			timeCol("updated_at"),
		},
		Indexes: []Index{
			{Name: "idx_certificates_fp", Columns: []string{"fingerprint"}},
			{Name: "idx_certificates_not_after", Columns: []string{"not_after"}},
		},
	}
}

func nodeCertificatesTable() Table {
	return Table{
		Name: "node_certificates",
		Columns: []Column{
			{Name: "node_id", Kind: Int64, NotNull: true, PK: true},
			bigint("cert_id", 0),
			bigint("revision", 0),
			strDef("key_fingerprint", 191, ""),
			strDef("applied_fingerprint", 191, ""),
			strDef("status", 32, "pending"),
			textDef("last_error", ""),
			optTime("requested_at"),
			optTime("applied_at"),
		},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"node_id"}, RefTable: "nodes", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Indexes: []Index{
			{Name: "idx_node_certificates_cert", Columns: []string{"cert_id"}},
		},
	}
}

func acmeAccountsTable() Table {
	return Table{
		Name: "acme_accounts",
		Columns: []Column{
			id(),
			str("ca_url", 255),
			strDef("email", 255, ""),
			text("key_cipher"),
			str("key_nonce", 128),
			strDef("account_url", 512, ""),
			timeCol("created_at"),
		},
		Indexes: []Index{
			{Name: "idx_acme_accounts_ca_email", Columns: []string{"ca_url", "email"}, Unique: true},
		},
	}
}

func nodeMetricsTable() Table {
	return Table{
		Name: "node_metrics",
		Columns: []Column{
			id(),
			{Name: "node_id", Kind: Int64, NotNull: true},
			timeCol("ts"),
			real("cpu_pct", 0),
			bigint("mem_used", 0),
			bigint("mem_total", 0),
			bigint("disk_used", 0),
			bigint("disk_total", 0),
			real("load1", 0),
			bigint("uptime_sec", 0),
			integer("online", 0),
			bigint("traffic_tx", 0),
			bigint("traffic_rx", 0),
			boolean("hy2_running", false),
		},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"node_id"}, RefTable: "nodes", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Indexes: []Index{
			{Name: "idx_metrics_node_ts", Columns: []string{"node_id", "ts"}},
		},
	}
}

func eventsTable() Table {
	return Table{
		Name: "events",
		Columns: []Column{
			id(),
			str("type", 64),
			bigint("node_id", 0),
			text("message"),
			strDef("severity", 16, "info"),
			timeCol("created_at"),
		},
		Indexes: []Index{
			{Name: "idx_events_created", Columns: []string{"created_at"}},
			{Name: "idx_events_node_created", Columns: []string{"node_id", "created_at"}},
		},
	}
}

func logsTable() Table {
	return Table{
		Name: "hy2_logs",
		Columns: []Column{
			id(),
			{Name: "node_id", Kind: Int64, NotNull: true},
			timeCol("ts"),
			strDef("level", 16, ""),
			strDef("logger", 64, ""),
			textDef("message", ""),
			textDef("raw", ""),
			strDef("event", 64, ""),
			strDef("client_ip", 64, ""),
			strDef("user_id", 191, ""),
			strDef("host", 255, ""),
			integer("port", 0),
			bigint("bytes_tx", 0),
			bigint("bytes_rx", 0),
			bigint("duration_ms", 0),
			textDef("error", ""),
		},
		Indexes: []Index{
			{Name: "idx_logs_ts", Columns: []string{"ts"}},
			{Name: "idx_logs_node_ts", Columns: []string{"node_id", "ts"}},
			{Name: "idx_logs_event_ts", Columns: []string{"event", "ts"}},
			{Name: "idx_logs_ip_ts", Columns: []string{"client_ip", "ts"}},
			{Name: "idx_logs_user_ts", Columns: []string{"user_id", "ts"}},
		},
	}
}

func logBatchesTable() Table {
	return Table{
		Name: "log_batches",
		Columns: []Column{
			{Name: "node_id", Kind: Int64, NotNull: true},
			str("batch_id", 64),
			timeCol("received_at"),
		},
		PrimaryKey: []string{"node_id", "batch_id"},
	}
}

func statsBatchesTable() Table {
	return Table{
		Name: "stats_batches",
		Columns: []Column{
			{Name: "node_id", Kind: Int64, NotNull: true},
			str("batch_id", 64),
			timeCol("received_at"),
		},
		PrimaryKey: []string{"node_id", "batch_id"},
	}
}

func settingsTable() Table {
	return Table{
		Name: "settings",
		Columns: []Column{
			{Name: "setting_key", Kind: String, Size: 191, NotNull: true, PK: true},
			{Name: "value", Kind: Text, NotNull: true},
		},
	}
}

func apiKeysTable() Table {
	return Table{
		Name: "api_keys",
		Columns: []Column{
			id(),
			str("name", 191),
			str("prefix", 32),
			unique(str("token_hash", 64)),
			textDef("scopes", ""),
			integer("rate_limit", 60),
			boolean("enabled", true),
			timeCol("created_at"),
			optTime("last_used_at"),
			optTime("expires_at"),
		},
		Indexes: []Index{
			{Name: "idx_api_keys_hash", Columns: []string{"token_hash"}},
		},
	}
}
