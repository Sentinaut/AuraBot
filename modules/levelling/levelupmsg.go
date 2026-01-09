package levelling

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type levelUpMsgRow struct {
	UserID    string
	Username  string
	ChannelID string
	MessageID string
	Content   string
	CreatedAt int64
}

/* =========================
   DB helpers
   ========================= */

func (m *Module) getLevelUpMessage(userID string, level int) (*levelUpMsgRow, error) {
	var r levelUpMsgRow
	err := m.db.QueryRow(
		`SELECT user_id, username, channel_id, message_id, content, created_at
		 FROM level_up_messages
		 WHERE user_id = ? AND level = ?`,
		userID, level,
	).Scan(&r.UserID, &r.Username, &r.ChannelID, &r.MessageID, &r.Content, &r.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (m *Module) deleteLevelUpMessage(userID string, level int) (int64, error) {
	res, err := m.db.Exec(
		`DELETE FROM level_up_messages WHERE user_id = ? AND level = ?`,
		userID, level,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

/* =========================
   /levelupmsg
   ========================= */

func (m *Module) handleLevelUpMsg(s *discordgo.Session, i *discordgo.InteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[levelling] panic in /levelupmsg: %v", r)
			m.respondEphemeral(s, i, "Something went wrong handling that command. Try again.")
		}
	}()

	// Default: only invoker can see the response
	visible := false

	respond := func(msg string) {
		flags := discordgo.MessageFlagsEphemeral
		if visible {
			flags = 0
		}
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: msg,
				Flags:   flags,
			},
		})
	}

	respondEmbed := func(embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
		flags := discordgo.MessageFlagsEphemeral
		if visible {
			flags = 0
		}
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{embed},
				Components: components,
				Flags:      flags,
			},
		})
	}

	guildID := strings.TrimSpace(i.GuildID)
	if guildID == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// default target = invoker
	target := (*discordgo.User)(nil)
	if i.Member != nil && i.Member.User != nil {
		target = i.Member.User
	} else if i.User != nil {
		target = i.User
	}
	if target == nil {
		m.respondEphemeral(s, i, "Could not determine user.")
		return
	}

	level := 0
	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "user":
			if u := opt.UserValue(s); u != nil {
				target = u
			}
		case "level":
			level = int(opt.IntValue())
		case "visible":
			visible = opt.BoolValue()
		}
	}
	if level <= 0 {
		respond("Level must be 1 or higher.")
		return
	}

	row, err := m.getLevelUpMessage(target.ID, level)
	if err != nil {
		respond("DB error reading saved level-up message.")
		return
	}
	if row == nil {
		respond(fmt.Sprintf("No saved level-up message found for <@%s> at **Level %d**.", target.ID, level))
		return
	}

	jump := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, row.ChannelID, row.MessageID)

	// Try to fetch the live message
	var (
		msg       *discordgo.Message
		fetchedOK bool
	)
	if row.ChannelID != "" && row.MessageID != "" {
		if live, err := s.ChannelMessage(row.ChannelID, row.MessageID); err == nil && live != nil {
			msg = live
			fetchedOK = true
		}
	}

	channelName := "unknown-channel"
	if ch, err := s.State.Channel(row.ChannelID); err == nil && ch != nil && ch.Name != "" {
		channelName = ch.Name
	} else if ch, err := s.Channel(row.ChannelID); err == nil && ch != nil && ch.Name != "" {
		channelName = ch.Name
	}

	displayName := target.Username
	avatarURL := target.AvatarURL("128")
	if mem, err := s.GuildMember(guildID, target.ID); err == nil && mem != nil && strings.TrimSpace(mem.Nick) != "" {
		displayName = mem.Nick
	}

	msgTime := time.Unix(row.CreatedAt, 0)
	content := strings.TrimSpace(row.Content)
	imageURL := ""

	if fetchedOK && msg != nil {
		if msg.Author != nil {
			avatarURL = msg.Author.AvatarURL("128")
			displayName = msg.Author.Username
			if mem, err := s.GuildMember(guildID, msg.Author.ID); err == nil && mem != nil && strings.TrimSpace(mem.Nick) != "" {
				displayName = mem.Nick
			}
		}
		if !msg.Timestamp.IsZero() {
			msgTime = msg.Timestamp
		}

		content = strings.TrimSpace(msg.Content)
		imageURL = firstImageAttachmentURL(msg)
		if imageURL == "" {
			imageURL = firstEmbedImageURL(msg)
		}
		if imageURL != "" {
			content = strings.TrimSpace(stripURLOnlyLines(content))
		}
	}

	header := fmt.Sprintf("%s • %s • Level %d", displayName, msgTime.Local().Format("02/01/2006 15:04"), level)

	embed := &discordgo.MessageEmbed{
		Color: 0x2B2D31,
		Author: &discordgo.MessageEmbedAuthor{
			Name:    header,
			IconURL: avatarURL,
			URL:     jump,
		},
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("#%s", channelName)},
	}

	if content != "" {
		embed.Description = truncateRunes(content, 3900)
	}
	if imageURL != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: imageURL}
	}
	if !fetchedOK {
		embed.Footer.Text = fmt.Sprintf("#%s • (original message unavailable)", channelName)
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Style: discordgo.LinkButton, Label: "Jump to message", URL: jump},
		}},
	}

	respondEmbed(embed, components)
}

/* =========================
   /levelupmsgdelete (ADMIN)
   ========================= */

func (m *Module) handleLevelUpMsgDelete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := strings.TrimSpace(i.GuildID)
	if guildID == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// Admin-only: Manage Server or Administrator
	var perms int64
	if i.Member != nil {
		perms = i.Member.Permissions
	}
	if perms&(discordgo.PermissionManageGuild|discordgo.PermissionAdministrator) == 0 {
		m.respondEphemeral(s, i, "You need **Manage Server** (or Administrator) to use this.")
		return
	}

	level := 0
	var target *discordgo.User

	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "user":
			target = opt.UserValue(s)
		case "level":
			level = int(opt.IntValue())
		}
	}

	if target == nil || target.ID == "" {
		m.respondEphemeral(s, i, "Missing user.")
		return
	}
	if level <= 0 {
		m.respondEphemeral(s, i, "Level must be 1 or higher.")
		return
	}

	deleted, err := m.deleteLevelUpMessage(target.ID, level)
	if err != nil {
		log.Printf("[levelling] levelupmsgdelete failed: %v", err)
		m.respondEphemeral(s, i, "DB error deleting saved level-up message.")
		return
	}

	if deleted == 0 {
		m.respondEphemeral(s, i, fmt.Sprintf("Nothing to delete: no saved level-up message for <@%s> at **Level %d**.", target.ID, level))
		return
	}

	m.respondEphemeral(s, i, fmt.Sprintf("✅ Deleted saved level-up message for <@%s> at **Level %d**.", target.ID, level))
}

/* =========================
   /levelupmsgset (ADMIN)
   ========================= */

func (m *Module) handleLevelUpMsgSet(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := strings.TrimSpace(i.GuildID)
	if guildID == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// Admin-only: Manage Server or Administrator
	var perms int64
	if i.Member != nil {
		perms = i.Member.Permissions
	}
	if perms&(discordgo.PermissionManageGuild|discordgo.PermissionAdministrator) == 0 {
		m.respondEphemeral(s, i, "You need **Manage Server** (or Administrator) to use this.")
		return
	}

	level := 0
	var target *discordgo.User
	link := ""

	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "level":
			level = int(opt.IntValue())
		case "user":
			target = opt.UserValue(s)
		case "message_link":
			if v, ok := opt.Value.(string); ok {
				link = strings.TrimSpace(v)
			}
		}
	}

	if level <= 0 {
		m.respondEphemeral(s, i, "Level must be 1 or higher.")
		return
	}
	if target == nil || target.ID == "" {
		m.respondEphemeral(s, i, "Missing user.")
		return
	}
	if link == "" {
		m.respondEphemeral(s, i, "Missing message_link.")
		return
	}

	chID, msgID, err := parseDiscordMessageLink(link)
	if err != nil {
		m.respondEphemeral(s, i, "That doesn’t look like a valid Discord message link.\nExample: `https://discord.com/channels/<guild>/<channel>/<message>`")
		return
	}

	// Must be same guild (best-effort check)
	parts := strings.Split(strings.TrimRight(link, "/"), "/")
	if len(parts) >= 3 {
		gFromLink := parts[len(parts)-3]
		if gFromLink != "" && gFromLink != "@me" && gFromLink != guildID {
			m.respondEphemeral(s, i, "That message link is from a different server.")
			return
		}
	}

	msg, err := s.ChannelMessage(chID, msgID)
	if err != nil || msg == nil {
		m.respondEphemeral(s, i, "I couldn’t access that message. Make sure the link is correct and I can read that channel.")
		return
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" {
		switch {
		case len(msg.Attachments) > 0:
			content = "(no text — contains attachment(s))"
		case len(msg.Embeds) > 0:
			content = "(no text — contains embed(s))"
		default:
			content = "(no text content)"
		}
	}

	now := time.Now().Unix()

	if err := m.saveLevelUpMessage(target.ID, target.Username, level, chID, msgID, content, now); err != nil {
		log.Printf("[levelling] levelupmsgset save failed: %v", err)
		m.respondEphemeral(s, i, "DB error saving level-up message.")
		return
	}

	jump := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, chID, msgID)
	m.respondEphemeral(s, i, fmt.Sprintf("✅ Saved level-up message for <@%s> (**%s**) at **Level %d**.\n%s", target.ID, target.Username, level, jump))
}

func parseDiscordMessageLink(link string) (channelID string, messageID string, err error) {
	u := strings.TrimSpace(link)
	u = strings.TrimRight(u, "/")

	if !strings.Contains(u, "/channels/") {
		return "", "", fmt.Errorf("missing /channels/")
	}

	parts := strings.Split(u, "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("too short")
	}

	messageID = parts[len(parts)-1]
	channelID = parts[len(parts)-2]

	if channelID == "" || messageID == "" {
		return "", "", fmt.Errorf("missing ids")
	}
	for _, c := range channelID {
		if c < '0' || c > '9' {
			return "", "", fmt.Errorf("bad channel id")
		}
	}
	for _, c := range messageID {
		if c < '0' || c > '9' {
			return "", "", fmt.Errorf("bad message id")
		}
	}
	return channelID, messageID, nil
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func firstImageAttachmentURL(msg *discordgo.Message) string {
	if msg == nil {
		return ""
	}
	for _, a := range msg.Attachments {
		if a == nil {
			continue
		}
		if a.ContentType != "" && strings.HasPrefix(a.ContentType, "image/") {
			return a.URL
		}
		name := strings.ToLower(a.Filename)
		if strings.HasSuffix(name, ".png") ||
			strings.HasSuffix(name, ".jpg") ||
			strings.HasSuffix(name, ".jpeg") ||
			strings.HasSuffix(name, ".gif") ||
			strings.HasSuffix(name, ".webp") {
			return a.URL
		}
	}
	return ""
}

func firstEmbedImageURL(msg *discordgo.Message) string {
	if msg == nil {
		return ""
	}
	for _, em := range msg.Embeds {
		if em == nil {
			continue
		}
		if em.Image != nil && strings.TrimSpace(em.Image.URL) != "" {
			return em.Image.URL
		}
		if em.Thumbnail != nil && strings.TrimSpace(em.Thumbnail.URL) != "" {
			return em.Thumbnail.URL
		}
	}
	return ""
}

func stripURLOnlyLines(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if isJustURL(t) {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func isJustURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return !strings.ContainsAny(s, " \t")
	}
	return false
}
