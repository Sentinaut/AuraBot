package autoroles

import (
	"database/sql"
	"fmt"
	"time"
)

func (m *Module) storeUpsert(guildID, channelID, messageID, emojiKey, emojiAPI, roleID string) error {
	_, err := m.db.Exec(
		`INSERT INTO autoroles(guild_id, channel_id, message_id, emoji_key, emoji_api, role_id, created_at)
		 VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(guild_id, message_id, emoji_key) DO UPDATE SET
		   channel_id = excluded.channel_id,
		   emoji_api  = excluded.emoji_api,
		   role_id    = excluded.role_id`,
		guildID, channelID, messageID, emojiKey, emojiAPI, roleID, time.Now().Unix(),
	)
	return err
}

func (m *Module) storeLookupRole(guildID, messageID, emojiKey string) (string, error) {
	var roleID string
	err := m.db.QueryRow(
		`SELECT role_id FROM autoroles WHERE guild_id = ? AND message_id = ? AND emoji_key = ?`,
		guildID, messageID, emojiKey,
	).Scan(&roleID)

	if err == sql.ErrNoRows {
		return "", nil
	}
	return roleID, err
}

func (m *Module) storeListEmojiAPIs(guildID, messageID string) ([]string, error) {
	rows, err := m.db.Query(
		`SELECT emoji_api FROM autoroles WHERE guild_id = ? AND message_id = ?`,
		guildID, messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, 4)
	for rows.Next() {
		var api string
		if err := rows.Scan(&api); err != nil {
			return nil, err
		}
		if api != "" {
			out = append(out, api)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Module) storeDeleteForMessage(guildID, messageID string) (int64, error) {
	res, err := m.db.Exec(
		`DELETE FROM autoroles WHERE guild_id = ? AND message_id = ?`,
		guildID, messageID,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
