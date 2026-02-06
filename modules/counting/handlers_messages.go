package counting

import (
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil || e.Author == nil {
		return
	}
	if e.Author.Bot {
		return
	}

	mode := m.channelMode(e.ChannelID)
	if mode == modeDisabled {
		return
	}

	n, ok := parseLeadingInt(e.Content)
	if !ok {
		// Not a counting attempt; ignore.
		return
	}

	res, err := m.applyCount(mode, e.GuildID, e.ChannelID, e.Author.ID, e.Author.Username, e.ID, n)
	if err != nil {
		log.Printf("[counting] apply error: %v", err)
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
		return
	}

	if res.OK {
		// ✅ normal vs ☑️ high score
		if res.HighScore {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHighScore)
		} else {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactOK)
		}

		// 💯 at 100
		if res.Count == 100 {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHundred)
		}

		// custom milestone emojis
		switch res.Count {
		case 200:
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji200)
		case 500:
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji500)
		case 1000:
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji1000)
		}

		return
	}

	_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)

	// Announce and punish
	if res.RuinedAt > 0 {
		// Custom reaction for specific user
		if e.Author.ID == customRuinerUserID {
			msg := fmt.Sprintf(
				"<@%s> ruined the count again... shock.\nThe count was **%d**. Next number is **1**.",
				e.Author.ID,
				res.RuinedAt,
			)
			_, _ = s.ChannelMessageSend(e.ChannelID, msg)
			_, _ = s.ChannelMessageSend(e.ChannelID, customRuinerGIFURL)
		} else {
			// Requested format: second line for Next number + reason
			msg := fmt.Sprintf(
				"<@%s> **RUINED IT AT %d!!**\nNext number is **1**. %s",
				e.Author.ID,
				res.RuinedAt,
				res.Reason,
			)
			_, _ = s.ChannelMessageSend(e.ChannelID, msg)
		}
	}

	m.punish(s, e.GuildID, e.Author.ID)
}

// If a message is edited in a counting channel:
// - If it becomes a number (e.g. "hello" -> "27"), announce it and remind the next number.
// - If it is the latest count message, also announce that they edited the count.
func (m *Module) onMessageUpdate(s *discordgo.Session, e *discordgo.MessageUpdate) {
	if e == nil {
		return
	}
	if m.channelMode(e.ChannelID) == modeDisabled {
		return
	}
	if m.db == nil {
		return
	}

	// Always fetch the message so we have the final content (update events can be partial)
	msg, err := s.ChannelMessage(e.ChannelID, e.ID)
	if err != nil || msg == nil || msg.Author == nil {
		return
	}
	if msg.Author.Bot {
		return
	}

	// Only care if the edited message NOW starts with a number
	editedNum, ok := parseLeadingInt(msg.Content)
	if !ok {
		return
	}

	// Read current channel state so we can say what the next number is
	var lastCount int64
	var lastMsgID string
	err = m.db.QueryRow(
		`SELECT last_count, last_message_id
		 FROM counting_state
		 WHERE channel_id = ?;`,
		e.ChannelID,
	).Scan(&lastCount, &lastMsgID)
	if err != nil {
		return
	}

	next := lastCount + 1

	// If they edited the latest count message, call it out specifically
	if lastMsgID != "" && lastMsgID == e.ID {
		txt := fmt.Sprintf(
			"<@%s> has edited their count because they think it's funny.\nThe next number is **%d**",
			msg.Author.ID,
			next,
		)
		_, _ = s.ChannelMessageSend(e.ChannelID, txt)
		return
	}

	// Otherwise, they edited SOME message into a number (e.g. "hello" -> "27")
	txt := fmt.Sprintf(
		"<@%s> has edited their message to **%d**.\nThe next number is **%d**",
		msg.Author.ID,
		editedNum,
		next,
	)
	_, _ = s.ChannelMessageSend(e.ChannelID, txt)
}

// Remove user-added ✅ / ☑️ so nobody can fake a valid count.
func (m *Module) onMessageReactionAdd(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if e == nil {
		return
	}
	if m.channelMode(e.ChannelID) == modeDisabled {
		return
	}

	// If bot isn't known yet, just skip.
	botID := ""
	if s != nil && s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}

	// Only remove reactions added by non-bot users
	if e.UserID == "" || (botID != "" && e.UserID == botID) {
		return
	}

	// In this discordgo version, Emoji is a VALUE, not a pointer.
	emojiName := e.Emoji.Name

	// Remove user-added ticks (and high-score tick).
	// (We keep ❌ alone; users can react with it if they want.)
	if emojiName == reactOK || emojiName == reactHighScore {
		_ = s.MessageReactionRemove(e.ChannelID, e.MessageID, emojiName, e.UserID)
	}
}
