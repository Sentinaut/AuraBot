package db

import (
	"database/sql"
)

// Migrate ensures the SQLite schema is up-to-date.
// This bot is single-server for XP + level-up messages (no guild_id there),
// but autoroles remains guild-scoped.
func Migrate(d *sql.DB) error {
	// SQLiteStudio convenience views; bot never relies on them.
	_, _ = d.Exec(`DROP VIEW IF EXISTS "User XP";`)
	_, _ = d.Exec(`DROP VIEW IF EXISTS "Level Up Messages";`)

	// Base schema (idempotent)
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS voting_threads (
			message_id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			thread_id  TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,

		`CREATE TABLE IF NOT EXISTS starboard_posts (
			original_message_id TEXT PRIMARY KEY,
			original_channel_id TEXT NOT NULL,
			starboard_message_id TEXT NOT NULL,
			starboard_channel_id TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,

		// Single-server XP table (NO guild_id)
		`CREATE TABLE IF NOT EXISTS user_xp (
			user_id    TEXT PRIMARY KEY,
			username   TEXT NOT NULL DEFAULT '',
			xp         INTEGER NOT NULL DEFAULT 0,
			last_xp_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_xp_xp ON user_xp(xp DESC);`,

		// Counting channels (per-channel state; survives restarts)
		`CREATE TABLE IF NOT EXISTS counting_state (
			channel_id   TEXT PRIMARY KEY,
			last_count   INTEGER NOT NULL DEFAULT 0,
			last_user_id TEXT NOT NULL DEFAULT '',
			prev_user_id TEXT NOT NULL DEFAULT '',
			updated_at   INTEGER NOT NULL DEFAULT 0
		);`,

		// Counting punishments (temporary role on mess-up)
		`CREATE TABLE IF NOT EXISTS counting_punishments (
			guild_id   TEXT NOT NULL,
			user_id    TEXT NOT NULL,
			role_id    TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			PRIMARY KEY (guild_id, user_id, role_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_counting_punishments_expires ON counting_punishments(expires_at);`,

		// Guild-scoped autoroles (keeps guild_id)
		`CREATE TABLE IF NOT EXISTS autoroles (
			guild_id   TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			emoji_key  TEXT NOT NULL,
			role_id    TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (guild_id, message_id, emoji_key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_autoroles_guild_message ON autoroles(guild_id, message_id);`,

		// Single-server level-up messages (NO guild_id)
		`CREATE TABLE IF NOT EXISTS level_up_messages (
			user_id    TEXT NOT NULL,
			username   TEXT NOT NULL DEFAULT '',
			level      INTEGER NOT NULL,
			channel_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			content    TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (user_id, level)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_level_up_messages_user ON level_up_messages(user_id);`,

		// ✅ NEW: join tracking (NO guild_id — single server design)
		`CREATE TABLE IF NOT EXISTS user_joins (
			user_id   TEXT PRIMARY KEY,
			username  TEXT NOT NULL DEFAULT '',
			joined_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_joins_joined_at ON user_joins(joined_at);`,
	}

	for _, q := range stmts {
		if _, err := d.Exec(q); err != nil {
			return err
		}
	}

	// Additive schema upgrades (safe on existing DBs)

	// Counting schema upgrades
	if err := ensureColumn(d, "counting_state", "prev_user_id", `ALTER TABLE counting_state ADD COLUMN prev_user_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(d, "counting_state", "updated_at", `ALTER TABLE counting_state ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}

	if err := ensureColumn(d, "starboard_posts", "author_id", `ALTER TABLE starboard_posts ADD COLUMN author_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(d, "starboard_posts", "stars_count", `ALTER TABLE starboard_posts ADD COLUMN stars_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(d, "autoroles", "emoji_api", `ALTER TABLE autoroles ADD COLUMN emoji_api TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}

	// Rebuild old experimental schemas that included guild_id
	if err := migrateUserXPToSingleServer(d); err != nil {
		return err
	}
	if err := migrateLevelUpMessagesToSingleServer(d); err != nil {
		return err
	}

	// Ensure indexes exist even after rebuilds
	_, _ = d.Exec(`CREATE INDEX IF NOT EXISTS idx_user_xp_xp ON user_xp(xp DESC);`)
	_, _ = d.Exec(`CREATE INDEX IF NOT EXISTS idx_level_up_messages_user ON level_up_messages(user_id);`)
	_, _ = d.Exec(`CREATE INDEX IF NOT EXISTS idx_user_joins_joined_at ON user_joins(joined_at);`)
	_, _ = d.Exec(`CREATE INDEX IF NOT EXISTS idx_counting_punishments_expires ON counting_punishments(expires_at);`)

	return nil
}

func ensureColumn(d *sql.DB, table, column, alterSQL string) error {
	ok, err := hasColumn(d, table, column)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = d.Exec(alterSQL)
	return err
}

func migrateUserXPToSingleServer(d *sql.DB) error {
	// If user_xp has guild_id, rebuild to remove it.
	hasGuild, err := hasColumn(d, "user_xp", "guild_id")
	if err != nil {
		return err
	}
	if !hasGuild {
		// Ensure username exists on older minimal schemas
		return ensureColumn(d, "user_xp", "username", `ALTER TABLE user_xp ADD COLUMN username TEXT NOT NULL DEFAULT ''`)
	}

	hasUsername, _ := hasColumn(d, "user_xp", "username")

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS user_xp_new (
			user_id    TEXT PRIMARY KEY,
			username   TEXT NOT NULL DEFAULT '',
			xp         INTEGER NOT NULL DEFAULT 0,
			last_xp_at INTEGER NOT NULL DEFAULT 0
		);
	`); err != nil {
		return err
	}

	if hasUsername {
		_, err = tx.Exec(`
			INSERT OR REPLACE INTO user_xp_new(user_id, username, xp, last_xp_at)
			SELECT user_id, MAX(username), MAX(xp), MAX(last_xp_at)
			FROM user_xp
			GROUP BY user_id;
		`)
	} else {
		_, err = tx.Exec(`
			INSERT OR REPLACE INTO user_xp_new(user_id, username, xp, last_xp_at)
			SELECT user_id, '', MAX(xp), MAX(last_xp_at)
			FROM user_xp
			GROUP BY user_id;
		`)
	}
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DROP TABLE user_xp;`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE user_xp_new RENAME TO user_xp;`); err != nil {
		return err
	}

	return tx.Commit()
}

func migrateLevelUpMessagesToSingleServer(d *sql.DB) error {
	// If level_up_messages has guild_id, rebuild to remove it.
	hasGuild, err := hasColumn(d, "level_up_messages", "guild_id")
	if err != nil {
		return err
	}
	if !hasGuild {
		return nil
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS level_up_messages_new (
			user_id    TEXT NOT NULL,
			username   TEXT NOT NULL DEFAULT '',
			level      INTEGER NOT NULL,
			channel_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			content    TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (user_id, level)
		);
	`); err != nil {
		return err
	}

	// Keep the most recent record per (user_id, level).
	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO level_up_messages_new(user_id, username, level, channel_id, message_id, content, created_at)
		SELECT user_id, MAX(username), level, MAX(channel_id), MAX(message_id), MAX(content), MAX(created_at)
		FROM level_up_messages
		GROUP BY user_id, level;
	`); err != nil {
		return err
	}

	if _, err := tx.Exec(`DROP TABLE level_up_messages;`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE level_up_messages_new RENAME TO level_up_messages;`); err != nil {
		return err
	}

	return tx.Commit()
}

func hasColumn(d *sql.DB, table, column string) (bool, error) {
	rows, err := d.Query(`PRAGMA table_info(` + table + `);`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		typ       string
		notnull   int
		dfltValue any
		pk        int
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
