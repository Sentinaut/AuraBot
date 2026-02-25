package welcoming

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

// Registers /toggleautoverify as a GUILD command.
// Fires on startup and when bot joins a guild.
func (m *Module) onGuildCreate(s *discordgo.Session, e *discordgo.GuildCreate) {
	if e == nil || e.Guild == nil || e.Guild.ID == "" {
		return
	}

	if s.State == nil || s.State.User == nil || s.State.User.ID == "" {
		log.Printf("[welcoming] cannot register /toggleautoverify: missing application ID")
		return
	}
	appID := s.State.User.ID

	cmd := &discordgo.ApplicationCommand{
		Name:        "toggleautoverify",
		Description: "Toggle auto-verify: roles + unverified removal vs nickname-only",
		DefaultMemberPermissions: func() *int64 {
			p := int64(discordgo.PermissionManageGuild)
			return &p
		}(),
		DMPermission: func() *bool { b := false; return &b }(),
	}

	_, err := s.ApplicationCommandCreate(appID, e.Guild.ID, cmd)
	if err != nil {
		log.Printf("[welcoming] failed to register /toggleautoverify in guild %s: %v", e.Guild.ID, err)
	}
}

func (m *Module) onGuildMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e == nil || e.User == nil || e.User.Bot {
		return
	}

	// ───── Give roles immediately on join ─────
	if m.unverifiedRoleID != "" {
		_ = s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.unverifiedRoleID)
	}
	if m.joinRoleID != "" {
		_ = s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.joinRoleID)
	}

	// ───── Welcome message (OLD STYLE RESTORED) ─────
	if m.welcomeChannelID != "" {

		memberCount := 0
		if g, err := s.State.Guild(e.GuildID); err == nil && g != nil {
			memberCount = g.MemberCount
		}

		embed := &discordgo.MessageEmbed{
			Title: "👋 Welcome!",
			Description: "Welcome <@" + e.User.ID + "> to Aura!\n\n" +
				"Head on over to <#" + m.onboardingChannelID + "> to begin.\n\n" +
				"React with 👋 to say hi!",
			Thumbnail: &discordgo.MessageEmbedThumbnail{
				URL: e.User.AvatarURL("256"),
			},
			Footer: &discordgo.MessageEmbedFooter{
				Text: "Member #" + itoa(memberCount),
			},
		}

		msg, err := s.ChannelMessageSendEmbed(m.welcomeChannelID, embed)
		if err != nil {
			log.Printf("[welcoming] failed to send welcome embed: %v", err)
		} else {
			// auto react 👋 like before
			_ = s.MessageReactionAdd(m.welcomeChannelID, msg.ID, "👋")
		}
	}

	// ───── Onboarding thread ─────
	if m.onboardingChannelID == "" {
		return
	}

	parent, err := s.ChannelMessageSend(
		m.onboardingChannelID,
		"<@"+e.User.ID+"> welcome to Aura!\n\nPlease reply in the thread below with the username you want (this will set your server nickname).",
	)
	if err != nil {
		log.Printf("[welcoming] failed to send onboarding parent message: %v", err)
		return
	}

	threadName := safeThreadName(e.User.Username)
	th, err := s.MessageThreadStart(m.onboardingChannelID, parent.ID, threadName, 1440)
	if err != nil {
		log.Printf("[welcoming] failed to start onboarding thread: %v", err)
		_ = s.ChannelMessageDelete(m.onboardingChannelID, parent.ID)
		return
	}

	m.mu.Lock()
	m.sessions[e.User.ID] = &onboardSession{
		GuildID:       e.GuildID,
		UserID:        e.User.ID,
		ParentMsgID:   parent.ID,
		ThreadID:      th.ID,
		NotifiedStaff: false,
	}
	m.mu.Unlock()

	_, _ = s.ChannelMessageSend(
		th.ID,
		"Reply here with the username you want. After you send it, you’ll be asked to confirm.",
	)
}

func (m *Module) onGuildMemberRemove(s *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e == nil || e.User == nil || e.User.Bot {
		return
	}

	m.mu.Lock()
	sess := m.sessions[e.User.ID]
	if sess != nil {
		delete(m.sessions, e.User.ID)
	}
	m.mu.Unlock()

	if sess == nil {
		return
	}

	if sess.ThreadID != "" {
		_, _ = s.ChannelDelete(sess.ThreadID)
	}
	if sess.ParentMsgID != "" && m.onboardingChannelID != "" {
		_ = s.ChannelMessageDelete(m.onboardingChannelID, sess.ParentMsgID)
	}
}
