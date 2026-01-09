package starboard

import (
	"context"
	"database/sql"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type ChannelRule struct {
	AutoReact bool
	Threshold int
}

type StarboardModule struct {
	rules         map[string]ChannelRule
	starboardChan string
	db            *sql.DB
}

func NewStarboard(rules map[string]ChannelRule, starboardChannelID string, db *sql.DB) *StarboardModule {
	norm := make(map[string]ChannelRule, len(rules))
	for ch, rule := range rules {
		ch = strings.TrimSpace(ch)
		if ch == "" {
			continue
		}
		if rule.Threshold <= 0 {
			rule.Threshold = 1
		}
		norm[ch] = rule
	}

	return &StarboardModule{
		rules:         norm,
		starboardChan: strings.TrimSpace(starboardChannelID),
		db:            db,
	}
}

func (m *StarboardModule) Name() string { return "starboard" }

func (m *StarboardModule) Register(s *discordgo.Session) error {
	s.AddHandler(m.onMessageCreate)
	s.AddHandler(m.onReactionAdd)
	s.AddHandler(m.onReactionRemove)
	s.AddHandler(m.onMessageDelete)
	s.AddHandler(m.onMessageDeleteBulk)
	return nil
}

func (m *StarboardModule) Start(ctx context.Context, s *discordgo.Session) error { return nil }

func (m *StarboardModule) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	// Auto-react limited to human posts
	if e == nil || e.Message == nil || e.Author == nil || e.Author.Bot {
		return
	}

	rule, ok := m.rules[e.ChannelID]
	if !ok {
		return
	}

	if !hasImage(e.Message) {
		return
	}

	if rule.AutoReact {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, "⭐")
	}
}

func (m *StarboardModule) onReactionAdd(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if e == nil || e.Emoji.Name != "⭐" {
		return
	}
	m.onStarChange(s, e.ChannelID, e.MessageID, e.GuildID)
}

func (m *StarboardModule) onReactionRemove(s *discordgo.Session, e *discordgo.MessageReactionRemove) {
	if e == nil || e.Emoji.Name != "⭐" {
		return
	}
	m.onStarChange(s, e.ChannelID, e.MessageID, e.GuildID)
}

func (m *StarboardModule) onStarChange(s *discordgo.Session, channelID, messageID, guildID string) {
	rule, ok := m.rules[channelID]
	if !ok {
		return
	}

	msg, err := s.ChannelMessage(channelID, messageID)
	if err != nil || msg == nil {
		return
	}

	// Allow other bots, but ignore OUR bot to prevent loops.
	if msg.Author != nil && msg.Author.Bot {
		if s.State != nil && s.State.User != nil && msg.Author.ID == s.State.User.ID {
			return
		}
	}

	if !hasImage(msg) {
		return
	}

	stars := countStars(msg)
	if stars < rule.Threshold {
		return
	}

	already, err := m.getStarboardMessageID(messageID)
	if err != nil || already != "" {
		return
	}

	imgURL := pickImageURL(msg)
	if imgURL == "" {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "⭐ Starboard",
		Description: "**" + safeUsername(msg.Author) + "** got **" + itoa(stars) + "** stars!",
		Color:       0xFFD700,
		URL:         makeJumpURL(guildID, channelID, messageID),
		Author: &discordgo.MessageEmbedAuthor{
			Name:    safeUsername(msg.Author),
			IconURL: safeAvatarURL(msg.Author),
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Click the title to jump to the original message",
		},
		Image: &discordgo.MessageEmbedImage{URL: imgURL},
	}

	if strings.TrimSpace(msg.Content) != "" {
		embed.Fields = []*discordgo.MessageEmbedField{
			{Name: "Message", Value: msg.Content},
		}
	}

	out, err := s.ChannelMessageSendEmbed(m.starboardChan, embed)
	if err != nil {
		log.Printf("[starboard] failed to post: %v", err)
		return
	}

	authorID := ""
	if msg.Author != nil {
		authorID = msg.Author.ID
	}

	_, err = m.db.Exec(
		`INSERT OR REPLACE INTO starboard_posts(
			original_message_id,
			original_channel_id,
			starboard_message_id,
			starboard_channel_id,
			author_id,
			stars_count,
			created_at
		) VALUES(?,?,?,?,?,?, strftime('%s','now'))`,
		messageID, channelID, out.ID, m.starboardChan, authorID, stars,
	)
	if err != nil {
		log.Printf("[starboard] db insert failed: %v", err)
	}
}

func (m *StarboardModule) onMessageDelete(s *discordgo.Session, e *discordgo.MessageDelete) {
	if e == nil {
		return
	}
	if _, ok := m.rules[e.ChannelID]; !ok {
		return
	}

	sbMsgID, err := m.getStarboardMessageID(e.ID)
	if err != nil || sbMsgID == "" {
		return
	}

	_ = s.ChannelMessageDelete(m.starboardChan, sbMsgID)
	_, _ = m.db.Exec(`DELETE FROM starboard_posts WHERE original_message_id = ?`, e.ID)
}

func (m *StarboardModule) onMessageDeleteBulk(s *discordgo.Session, e *discordgo.MessageDeleteBulk) {
	if e == nil {
		return
	}
	if _, ok := m.rules[e.ChannelID]; !ok {
		return
	}

	for _, mid := range e.Messages {
		sbMsgID, err := m.getStarboardMessageID(mid)
		if err != nil || sbMsgID == "" {
			continue
		}
		_ = s.ChannelMessageDelete(m.starboardChan, sbMsgID)
		_, _ = m.db.Exec(`DELETE FROM starboard_posts WHERE original_message_id = ?`, mid)
	}
}

func (m *StarboardModule) getStarboardMessageID(originalMessageID string) (string, error) {
	var sbMsgID string
	err := m.db.QueryRow(
		`SELECT starboard_message_id FROM starboard_posts WHERE original_message_id = ?`,
		originalMessageID,
	).Scan(&sbMsgID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return sbMsgID, err
}

func safeUsername(u *discordgo.User) string {
	if u == nil || u.Username == "" {
		return "Unknown"
	}
	return u.Username
}

func safeAvatarURL(u *discordgo.User) string {
	if u == nil {
		return ""
	}
	return u.AvatarURL("")
}
