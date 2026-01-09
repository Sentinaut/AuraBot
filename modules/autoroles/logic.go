package autoroles

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	switch i.ApplicationCommandData().Name {
	case "autorole":
		m.handleAutorole(s, i)
	case "autoremove":
		m.handleAutoremove(s, i)
	}
}

// ✅ Delete mapping rows when the underlying message is deleted (does NOT remove user roles)
func (m *Module) onMessageDelete(s *discordgo.Session, d *discordgo.MessageDelete) {
	if d == nil || d.GuildID == "" || d.ID == "" {
		return
	}

	deleted, err := m.storeDeleteForMessage(d.GuildID, d.ID)
	if err != nil {
		log.Printf("[autoroles] cleanup failed for deleted message %s: %v", d.ID, err)
		return
	}

	if deleted > 0 {
		log.Printf("[autoroles] cleaned %d autorole mapping(s) for deleted message %s", deleted, d.ID)
	}
}

// ---- permissions ----

func isStaff(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	perms := i.Member.Permissions
	return perms&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

func (m *Module) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// ---- /autorole ----

func (m *Module) handleAutorole(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isStaff(i) {
		m.respondEphemeral(s, i, "You need **Manage Server** or **Administrator** to use this command.")
		return
	}
	if strings.TrimSpace(i.GuildID) == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// defaults
	channelID := i.ChannelID
	text := "React to this message to get a role"

	var (
		messageID  string
		emojiInput string
		roleID     string
	)

	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "text":
			if v, ok := opt.Value.(string); ok && strings.TrimSpace(v) != "" {
				text = v
			}
		case "channel":
			ch := opt.ChannelValue(s)
			if ch != nil && ch.ID != "" {
				channelID = ch.ID
			}
		case "message_id":
			if v, ok := opt.Value.(string); ok && strings.TrimSpace(v) != "" {
				messageID = strings.TrimSpace(v)
			}
		case "emoji":
			if v, ok := opt.Value.(string); ok {
				emojiInput = strings.TrimSpace(v)
			}
		case "role":
			r := opt.RoleValue(s, i.GuildID)
			if r != nil && r.ID != "" {
				roleID = r.ID
			}
		}
	}

	if emojiInput == "" || roleID == "" {
		m.respondEphemeral(s, i, "Missing required emoji or role.")
		return
	}

	emojiKey, emojiAPI, err := parseEmojiInput(emojiInput)
	if err != nil {
		m.respondEphemeral(s, i, "Could not parse emoji. Use unicode ✅ or custom <:name:id>.")
		return
	}

	// message: existing or create new
	if messageID != "" {
		if _, err := s.ChannelMessage(channelID, messageID); err != nil {
			m.respondEphemeral(s, i, "I couldn't access that message in the selected channel. If it's in another channel, provide the channel option too.")
			return
		}
	} else {
		msg, err := s.ChannelMessageSend(channelID, text)
		if err != nil || msg == nil || msg.ID == "" {
			m.respondEphemeral(s, i, "Failed to post message in that channel.")
			return
		}
		messageID = msg.ID
	}

	// add reaction first; if this fails we save nothing
	if err := s.MessageReactionAdd(channelID, messageID, emojiAPI); err != nil {
		log.Printf("[autoroles] reaction add failed (channel=%s msg=%s emoji=%q): %v", channelID, messageID, emojiAPI, err)
		m.respondEphemeral(s, i, "I saved nothing because I couldn't add the reaction.\n"+"**Error:** "+err.Error())
		return
	}

	if err := m.storeUpsert(i.GuildID, channelID, messageID, emojiKey, emojiAPI, roleID); err != nil {
		log.Printf("[autoroles] upsert failed: %v", err)
		m.respondEphemeral(s, i, "DB error saving autorole.")
		return
	}

	link := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, channelID, messageID)
	m.respondEphemeral(s, i, "✅ Autorole saved.\nMessage: "+link)
}

// ---- /autoremove ----

func (m *Module) handleAutoremove(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isStaff(i) {
		m.respondEphemeral(s, i, "You need **Manage Server** or **Administrator** to use this command.")
		return
	}
	if strings.TrimSpace(i.GuildID) == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	channelID := i.ChannelID
	var messageID string

	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "channel":
			ch := opt.ChannelValue(s)
			if ch != nil && ch.ID != "" {
				channelID = ch.ID
			}
		case "message_id":
			if v, ok := opt.Value.(string); ok && strings.TrimSpace(v) != "" {
				messageID = strings.TrimSpace(v)
			}
		}
	}

	if messageID == "" {
		m.respondEphemeral(s, i, "Missing message_id.")
		return
	}

	emojiAPIs, err := m.storeListEmojiAPIs(i.GuildID, messageID)
	if err != nil {
		log.Printf("[autoroles] list emojis failed: %v", err)
		m.respondEphemeral(s, i, "DB error reading autoroles.")
		return
	}

	deleted, err := m.storeDeleteForMessage(i.GuildID, messageID)
	if err != nil {
		log.Printf("[autoroles] delete failed: %v", err)
		m.respondEphemeral(s, i, "DB error deleting autoroles.")
		return
	}

	// remove the bot's reactions if we can
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	if botID != "" {
		for _, em := range emojiAPIs {
			_ = s.MessageReactionRemove(channelID, messageID, em, botID)
		}
	}

	m.respondEphemeral(s, i, fmt.Sprintf("✅ Removed %d autorole mapping(s) from message %s.", deleted, messageID))
}

// ---- reaction handling (toggle) ----

func (m *Module) onReactionAdd(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if e == nil || e.UserID == "" || e.GuildID == "" || e.MessageID == "" || e.ChannelID == "" {
		return
	}

	// ignore our own reactions
	if s.State != nil && s.State.User != nil && e.UserID == s.State.User.ID {
		return
	}

	emojiKey := reactionToKey(e.Emoji)

	roleID, err := m.storeLookupRole(e.GuildID, e.MessageID, emojiKey)
	if err != nil {
		log.Printf("[autoroles] lookup failed: %v", err)
		return
	}
	if roleID == "" {
		return // not an autorole mapping
	}

	has, err := memberHasRole(s, e.GuildID, e.UserID, roleID)
	if err != nil {
		log.Printf("[autoroles] member fetch failed: %v", err)
		_ = s.MessageReactionRemove(e.ChannelID, e.MessageID, emojiToAPI(e.Emoji), e.UserID)
		return
	}

	// toggle
	if has {
		if err := s.GuildMemberRoleRemove(e.GuildID, e.UserID, roleID); err != nil {
			log.Printf("[autoroles] role remove failed: %v", err)
		}
	} else {
		if err := s.GuildMemberRoleAdd(e.GuildID, e.UserID, roleID); err != nil {
			log.Printf("[autoroles] role add failed: %v", err)
		}
	}

	// keep message clean: remove user's reaction, keep bot's
	_ = s.MessageReactionRemove(e.ChannelID, e.MessageID, emojiToAPI(e.Emoji), e.UserID)
	_ = s.MessageReactionAdd(e.ChannelID, e.MessageID, emojiToAPI(e.Emoji))
}

func memberHasRole(s *discordgo.Session, guildID, userID, roleID string) (bool, error) {
	mem, err := s.GuildMember(guildID, userID)
	if err != nil {
		return false, err
	}
	for _, r := range mem.Roles {
		if r == roleID {
			return true, nil
		}
	}
	return false, nil
}

// ---- emoji helpers ----

// Stable DB key:
// - custom: "id:<id>"
// - unicode: "name:<emoji>"
func reactionToKey(e discordgo.Emoji) string {
	if e.ID != "" {
		return "id:" + e.ID
	}
	return "name:" + e.Name
}

// Discord API format for reactions:
// - Unicode: "👍"
// - Custom: "name:id"
func emojiToAPI(e discordgo.Emoji) string {
	if e.ID != "" {
		return fmt.Sprintf("%s:%s", e.Name, e.ID)
	}
	return e.Name
}

// Input accepts:
// - Unicode: 👍
// - Custom: <:name:id> or <a:name:id>
func parseEmojiInput(input string) (emojiKey string, emojiAPI string, err error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return "", "", fmt.Errorf("empty")
	}

	// custom emoji: <a:name:id> or <:name:id>
	if strings.HasPrefix(in, "<") && strings.HasSuffix(in, ">") && strings.Contains(in, ":") {
		trim := strings.TrimSuffix(strings.TrimPrefix(in, "<"), ">") // a:name:id OR :name:id
		trim = strings.TrimPrefix(trim, "a:")
		trim = strings.TrimPrefix(trim, ":")

		parts := strings.Split(trim, ":")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			name := parts[0]
			id := parts[1]
			return "id:" + id, name + ":" + id, nil
		}
		return "", "", fmt.Errorf("bad custom emoji")
	}

	// unicode
	return "name:" + in, in, nil
}
