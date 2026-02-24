package welcoming

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

// Registers /toggleautoverify as a GUILD command.
// This fires on startup (for each guild) and whenever the bot joins a new guild.
func (m *Module) onGuildCreate(s *discordgo.Session, e *discordgo.GuildCreate) {
	if e == nil || e.Guild == nil || e.Guild.ID == "" {
		return
	}

	// Application ID for command registration (discordgo common pattern)
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
		// Ignore bots entirely (no roles, no welcome, no thread)
		return
	}

	// ───────────── Give roles immediately on join ─────────────
	if m.unverifiedRoleID != "" {
		if err := s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.unverifiedRoleID); err != nil {
			log.Printf("[welcoming] failed to add unverified role (user=%s role=%s): %v", e.User.ID, m.unverifiedRoleID, err)
		}
	}
	if m.joinRoleID != "" {
		if err := s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.joinRoleID); err != nil {
			log.Printf("[welcoming] failed to add join role (user=%s role=%s): %v", e.User.ID, m.joinRoleID, err)
		}
	}

	// ───────────── Welcome embed in #welcome ─────────────
	if m.welcomeChannelID != "" {
		memberCount := 0
		if g, err := s.State.Guild(e.GuildID); err == nil && g != nil {
			memberCount = g.MemberCount
		}

		embed := &discordgo.MessageEmbed{
			Title: "👋 Welcome!",
			Description: "Welcome to Aura! Please head over to the onboarding channel to set your username.\n\n" +
				"Once you've set your username, you'll be verified automatically (if enabled) and can access the server.",
			Footer: &discordgo.MessageEmbedFooter{
				Text: "Member #" + itoa(memberCount),
			},
		}

		if _, err := s.ChannelMessageSendEmbed(m.welcomeChannelID, embed); err != nil {
			log.Printf("[welcoming] failed to send welcome embed: %v", err)
		}
	}

	// ───────────── Onboarding thread in #onboarding ─────────────
	if m.onboardingChannelID == "" {
		return
	}

	parent, err := s.ChannelMessageSend(m.onboardingChannelID,
		"<@"+e.User.ID+"> welcome to Aura!\n\nPlease reply in the thread below with the username you want (this will set your server nickname).")
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
		GuildID:     e.GuildID,
		UserID:      e.User.ID,
		ParentMsgID: parent.ID,
		ThreadID:    th.ID,
	}
	m.mu.Unlock()

	if _, err := s.ChannelMessageSend(th.ID,
		"Reply here with the username you want. After you send it, you’ll be asked to confirm."); err != nil {
		log.Printf("[welcoming] failed to send thread instructions: %v", err)
	}
}

func (m *Module) onGuildMemberRemove(s *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e == nil || e.User == nil || e.User.Bot {
		return
	}

	// Cleanup if they leave before verifying:
	m.mu.Lock()
	sess := m.sessions[e.User.ID]
	if sess != nil {
		delete(m.sessions, e.User.ID)
	}
	m.mu.Unlock()

	if sess == nil {
		return
	}

	// Delete the thread and the parent message if possible.
	if sess.ThreadID != "" {
		if _, err := s.ChannelDelete(sess.ThreadID); err != nil {
			log.Printf("[welcoming] failed to delete onboarding thread on member remove: %v", err)
		}
	}
	if sess.ParentMsgID != "" && m.onboardingChannelID != "" {
		if err := s.ChannelMessageDelete(m.onboardingChannelID, sess.ParentMsgID); err != nil {
			log.Printf("[welcoming] failed to delete onboarding parent message on member remove: %v", err)
		}
	}
}
