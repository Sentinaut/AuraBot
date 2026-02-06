package counting

import (
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) punish(s *discordgo.Session, guildID, userID string) {
	if strings.TrimSpace(guildID) == "" {
		return
	}
	if strings.TrimSpace(m.ruinedRoleID) == "" {
		return
	}
	if m.ruinedFor <= 0 {
		return
	}

	// Assign role (requires Manage Roles and role hierarchy)
	if err := s.GuildMemberRoleAdd(guildID, userID, m.ruinedRoleID); err != nil {
		log.Printf("[counting] failed to add ruined role: %v", err)
	}

	expiresAt := time.Now().Add(m.ruinedFor).Unix()
	_, err := m.db.Exec(
		`INSERT INTO counting_punishments (guild_id, user_id, role_id, expires_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(guild_id, user_id, role_id) DO UPDATE SET
			expires_at = CASE
				WHEN excluded.expires_at > counting_punishments.expires_at THEN excluded.expires_at
				ELSE counting_punishments.expires_at
			END;`,
		guildID, userID, m.ruinedRoleID, expiresAt,
	)
	if err != nil {
		log.Printf("[counting] failed to store punishment expiry: %v", err)
	}
}

func (m *Module) cleanupExpired(s *discordgo.Session) {
	if m.db == nil {
		return
	}
	if strings.TrimSpace(m.ruinedRoleID) == "" {
		return
	}

	now := time.Now().Unix()

	rows, err := m.db.Query(
		`SELECT guild_id, user_id, role_id
		 FROM counting_punishments
		 WHERE expires_at <= ?;`,
		now,
	)
	if err != nil {
		log.Printf("[counting] cleanup query error: %v", err)
		return
	}
	defer rows.Close()

	type item struct {
		guildID string
		userID  string
		roleID  string
	}

	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.guildID, &it.userID, &it.roleID); err != nil {
			log.Printf("[counting] cleanup scan error: %v", err)
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[counting] cleanup rows error: %v", err)
		return
	}

	for _, it := range items {
		if it.guildID == "" || it.userID == "" || it.roleID == "" {
			continue
		}
		if err := s.GuildMemberRoleRemove(it.guildID, it.userID, it.roleID); err != nil {
			log.Printf("[counting] failed to remove expired role (continuing): %v", err)
		}
		_, _ = m.db.Exec(
			`DELETE FROM counting_punishments WHERE guild_id = ? AND user_id = ? AND role_id = ?;`,
			it.guildID, it.userID, it.roleID,
		)
	}
}
