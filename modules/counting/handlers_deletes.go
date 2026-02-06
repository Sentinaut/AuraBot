package counting

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onMessageDelete(s *discordgo.Session, e *discordgo.MessageDelete) {
	if e == nil {
		return
	}
	m.handleDeletedMessage(s, e.GuildID, e.ChannelID, e.ID)
}

func (m *Module) onMessageDeleteBulk(s *discordgo.Session, e *discordgo.MessageDeleteBulk) {
	if e == nil {
		return
	}
	for _, id := range e.Messages {
		m.handleDeletedMessage(s, e.GuildID, e.ChannelID, id)
	}
}

func (m *Module) handleDeletedMessage(s *discordgo.Session, guildID, channelID, messageID string) {
	// Only act in the 2 counting channels
	if m.channelMode(channelID) == modeDisabled {
		return
	}
	if m.db == nil || strings.TrimSpace(messageID) == "" {
		return
	}

	var lastCount int64
	var lastUserID string
	var lastMsgID string

	err := m.db.QueryRow(
		`SELECT last_count, last_user_id, last_message_id
		 FROM counting_state
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&lastCount, &lastUserID, &lastMsgID)

	if err != nil {
		// no rows / schema mismatch / etc -> ignore
		return
	}

	if lastMsgID == "" || lastMsgID != messageID {
		return
	}

	next := lastCount + 1
	if lastUserID == "" {
		return
	}

	msg := fmt.Sprintf("<@%s> has deleted their count, the next number is **%d**.", lastUserID, next)
	_, _ = s.ChannelMessageSend(channelID, msg)
}

// Adds counting_state.last_message_id if missing (required for delete announcements).
func (m *Module) ensureDeleteTrackingSchema() {
	if m.db == nil {
		return
	}

	// SQLite: no IF NOT EXISTS for ADD COLUMN, so ignore "duplicate column name" errors.
	_, err := m.db.Exec(`ALTER TABLE counting_state ADD COLUMN last_message_id TEXT NOT NULL DEFAULT '';`)
	if err != nil {
		// Already exists or table missing (table is owned by migrate.go). Both are safe to ignore here.
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			// If migrate hasn't run yet, you'll see "no such table". That's fine; it'll exist after migrations.
			if !strings.Contains(strings.ToLower(err.Error()), "no such table") {
				log.Printf("[counting] ensure schema warning: %v", err)
			}
		}
	}
}
