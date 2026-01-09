package welcoming

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Module struct {
	channelID string
	roleID    string // optional auto-role
}

func New(welcomeChannelID string, autoRoleID string) *Module {
	return &Module{
		channelID: strings.TrimSpace(welcomeChannelID),
		roleID:    strings.TrimSpace(autoRoleID),
	}
}

func (m *Module) Name() string { return "welcoming" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onGuildMemberAdd)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error { return nil }

func (m *Module) onGuildMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e == nil || e.User == nil {
		return
	}
	if m.channelID == "" {
		return
	}

	// Optional auto-role
	if m.roleID != "" {
		if err := s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.roleID); err != nil {
			log.Printf("[welcoming] failed to add role: %v", err)
		}
	}

	memberCount := 0
	if g, err := s.State.Guild(e.GuildID); err == nil && g != nil {
		memberCount = g.MemberCount
	}

	embed := &discordgo.MessageEmbed{
		Title: "👋 Welcome!",
		Description: "Welcome <@" + e.User.ID + "> to **Aura**!\n\n" +
			"React with 👋 to say hi",
		Color: 0x9B59B6,
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: e.User.AvatarURL(""),
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Member #" + itoa(memberCount),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	msg, err := s.ChannelMessageSendEmbed(m.channelID, embed)
	if err != nil {
		log.Printf("[welcoming] failed to send welcome message: %v", err)
		return
	}

	// Auto-react with 👋 so users can just click it
	if err := s.MessageReactionAdd(m.channelID, msg.ID, "👋"); err != nil {
		log.Printf("[welcoming] failed to add wave reaction: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + (n % 10))
		n /= 10
	}
	return string(b[i:])
}
