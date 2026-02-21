package welcoming

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Module struct {
	welcomeChannelID    string
	onboardingChannelID string
	memberRoleID        string // granted AFTER username confirmed

	// If true: auto-grant member role + remove unverified after username confirmation.
	// If false: ONLY set nickname; staff handles roles manually.
	autoVerifyEnabled bool

	// Roles granted immediately on join:
	unverifiedRoleID string // removed after username confirmed
	joinRoleID       string // stays

	mu       sync.Mutex
	sessions map[string]*onboardSession // key = userID
}

type onboardSession struct {
	GuildID       string
	UserID        string
	ParentMsgID   string
	ThreadID      string
	CandidateName string
}

func New(welcomeChannelID, onboardingChannelID, memberRoleID, unverifiedRoleID, joinRoleID string) *Module {
	return &Module{
		welcomeChannelID:    strings.TrimSpace(welcomeChannelID),
		onboardingChannelID: strings.TrimSpace(onboardingChannelID),
		memberRoleID:        strings.TrimSpace(memberRoleID),

		// Injected from main.go:
		unverifiedRoleID: strings.TrimSpace(unverifiedRoleID),
		joinRoleID:       strings.TrimSpace(joinRoleID),

		autoVerifyEnabled: envBoolDefault("WELCOMING_AUTOVERIFY_DEFAULT", true),

		sessions: make(map[string]*onboardSession),
	}
}

func (m *Module) Name() string { return "welcoming" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onReady)
	s.AddHandler(m.onGuildMemberAdd)
	s.AddHandler(m.onGuildMemberRemove) // cleanup if they leave before verify
	s.AddHandler(m.onMessageCreate)
	s.AddHandler(m.onInteractionCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error { return nil }

func (m *Module) onReady(s *discordgo.Session, r *discordgo.Ready) {
	appID := ""
	if s.State != nil && s.State.User != nil {
		appID = s.State.User.ID
	}
	if appID == "" {
		log.Println("[welcoming] cannot register commands: missing application ID")
		return
	}

	cmd := &discordgo.ApplicationCommand{
		Name:        "toggleautoverify",
		Description: "Toggle auto-verify: roles + unverified removal vs nickname-only",
		DefaultMemberPermissions: func() *int64 {
			p := int64(discordgo.PermissionManageGuild)
			return &p
		}(),
		DMPermission: func() *bool { b := false; return &b }(),
	}

	// Register per-guild for fast availability.
	if s.State == nil || len(s.State.Guilds) == 0 {
		log.Println("[welcoming] cannot register commands: no guilds in state yet")
		return
	}

	for _, g := range s.State.Guilds {
		if g == nil || g.ID == "" {
			continue
		}
		if _, err := s.ApplicationCommandCreate(appID, g.ID, cmd); err != nil {
			log.Printf("[welcoming] failed to register /toggleautoverify in guild %s: %v", g.ID, err)
		}
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
				"Once you've set your username, you'll be verified automatically (if enabled) and can chat with everyone.",
			Footer: &discordgo.MessageEmbedFooter{
				Text: "Member #" + itoa(memberCount),
			},
		}
		_, _ = s.ChannelMessageSendEmbed(m.welcomeChannelID, embed)
	}

	// ───────────── Create onboarding message + thread ─────────────
	if m.onboardingChannelID == "" {
		return
	}

	parentEmbed := &discordgo.MessageEmbed{
		Title:       "🧾 Username setup",
		Description: "<@" + e.User.ID + ">, a thread has been created for you. Please type your username in the thread below.",
	}

	msg, err := s.ChannelMessageSendEmbed(m.onboardingChannelID, parentEmbed)
	if err != nil || msg == nil {
		log.Printf("[welcoming] failed to send onboarding parent message: %v", err)
		return
	}

	threadName := safeThreadName("Username: " + e.User.Username)
	thread, err := s.MessageThreadStartComplex(m.onboardingChannelID, msg.ID, &discordgo.ThreadStart{
		Name:                threadName,
		AutoArchiveDuration: 60,
	})
	if err != nil || thread == nil {
		log.Printf("[welcoming] failed to create onboarding thread: %v", err)
		_ = s.ChannelMessageDelete(m.onboardingChannelID, msg.ID)
		return
	}

	m.mu.Lock()
	m.sessions[e.User.ID] = &onboardSession{
		GuildID:     e.GuildID,
		UserID:      e.User.ID,
		ParentMsgID: msg.ID,
		ThreadID:    thread.ID,
	}
	m.mu.Unlock()

	_, _ = s.ChannelMessageSend(thread.ID, "Please type in your username below")
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

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Author == nil || e.Author.Bot {
		return
	}
	if e.GuildID == "" {
		return
	}

	// Only care about messages in onboarding threads we created
	m.mu.Lock()
	var sess *onboardSession
	for _, v := range m.sessions {
		if v != nil && v.ThreadID == e.ChannelID {
			sess = v
			break
		}
	}
	m.mu.Unlock()

	if sess == nil {
		return
	}

	content := strings.TrimSpace(e.Content)
	if content == "" {
		return
	}

	// Save candidate username
	m.mu.Lock()
	sess.CandidateName = content
	m.mu.Unlock()

	sendConfirm(s, sess.ThreadID, content, sess.UserID)
}

func sendConfirm(s *discordgo.Session, channelID, name, userID string) {
	embed := &discordgo.MessageEmbed{
		Title:       "Confirm username",
		Description: "Set your username to:\n\n**" + escapeMarkdown(name) + "**\n\nIs this correct?",
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Style:    discordgo.SuccessButton,
					Label:    "Yes",
					CustomID: "welcoming:yes:" + userID,
				},
				discordgo.Button{
					Style:    discordgo.DangerButton,
					Label:    "No",
					CustomID: "welcoming:no:" + userID,
				},
			},
		},
	}

	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds:         []*discordgo.MessageEmbed{embed},
		Components:     components,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
	if err != nil {
		log.Printf("[welcoming] failed to send confirm embed: %v", err)
	}
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		if data.Name != "toggleautoverify" {
			return
		}

		m.mu.Lock()
		m.autoVerifyEnabled = !m.autoVerifyEnabled
		enabled := m.autoVerifyEnabled
		m.mu.Unlock()

		status := "OFF"
		details := "Nickname-only (staff handles roles)."
		if enabled {
			status = "ON"
			details = "Auto-verify (give members role + remove unverified)."
		}

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Auto-verify is now **" + status + "** — " + details,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return

	case discordgo.InteractionMessageComponent:
		data := i.MessageComponentData()
		parts := strings.Split(data.CustomID, ":")
		if len(parts) != 3 || parts[0] != "welcoming" {
			return
		}

		action := parts[1]
		userID := parts[2]

		// Only the target user can click
		if i.Member == nil || i.Member.User == nil || i.Member.User.ID != userID {
			_ = s.InteractionRespond(i.Interaction, ephemeral("Those buttons aren’t for you 🙂"))
			return
		}

		m.mu.Lock()
		sess := m.sessions[userID]
		m.mu.Unlock()
		if sess == nil {
			_ = s.InteractionRespond(i.Interaction, ephemeral("This setup session has expired."))
			return
		}

		// Disable buttons on the message they clicked
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Components: disableAllButtons(i.Message.Components),
			},
		})

		switch action {
		case "no":
			m.mu.Lock()
			sess.CandidateName = ""
			m.mu.Unlock()

			_, _ = s.ChannelMessageSend(sess.ThreadID, "Please type in your username below")

		case "yes":
			m.mu.Lock()
			name := strings.TrimSpace(sess.CandidateName)
			m.mu.Unlock()

			if name == "" {
				_, _ = s.ChannelMessageSend(sess.ThreadID, "Please type in your username below")
				return
			}

			// Set nickname (ignore error if perms missing)
			if err := s.GuildMemberNickname(sess.GuildID, sess.UserID, name); err != nil {
				log.Printf("[welcoming] failed to set nickname: %v", err)
			}

			// Auto-verify toggle:
			m.mu.Lock()
			autoVerify := m.autoVerifyEnabled
			m.mu.Unlock()

			// If manual mode: mention @everyone for approval
			if !autoVerify {
				// Post in onboarding channel (fallback to thread if onboarding not set)
				targetChannelID := m.onboardingChannelID
				if targetChannelID == "" {
					targetChannelID = sess.ThreadID
				}

				content := "@everyone\n" + name + " has set their username and is awaiting approval"
				_, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
					Content: content,
					AllowedMentions: &discordgo.MessageAllowedMentions{
						Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeEveryone},
					},
				})
				if err != nil {
					log.Printf("[welcoming] failed to send @everyone manual-approval message: %v", err)
				}
			}

			if autoVerify {
				// Give member role
				if m.memberRoleID != "" {
					if err := s.GuildMemberRoleAdd(sess.GuildID, sess.UserID, m.memberRoleID); err != nil {
						log.Printf("[welcoming] failed to add member role: %v", err)
					}
				}

				// Remove unverified role after verification
				if m.unverifiedRoleID != "" {
					if err := s.GuildMemberRoleRemove(sess.GuildID, sess.UserID, m.unverifiedRoleID); err != nil {
						log.Printf("[welcoming] failed to remove unverified role: %v", err)
					}
				}
			}

			// Delete the onboarding thread (and then the parent message).
			if _, err := s.ChannelDelete(sess.ThreadID); err != nil {
				log.Printf("[welcoming] failed to delete onboarding thread: %v", err)

				// Fallback: archive + lock
				archived := true
				locked := true
				_, _ = s.ChannelEditComplex(sess.ThreadID, &discordgo.ChannelEdit{
					Archived: &archived,
					Locked:   &locked,
				})
			}

			if err := s.ChannelMessageDelete(m.onboardingChannelID, sess.ParentMsgID); err != nil {
				log.Printf("[welcoming] failed to delete onboarding parent message: %v", err)
			}

			// End session
			m.mu.Lock()
			delete(m.sessions, userID)
			m.mu.Unlock()
		}
		return

	default:
		return
	}
}

func ephemeral(msg string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

func disableAllButtons(rows []discordgo.MessageComponent) []discordgo.MessageComponent {
	out := make([]discordgo.MessageComponent, 0, len(rows))

	for _, row := range rows {
		ar, ok := row.(discordgo.ActionsRow)
		if !ok {
			if ptr, ok2 := row.(*discordgo.ActionsRow); ok2 && ptr != nil {
				ar = *ptr
				ok = true
			}
		}
		if !ok {
			out = append(out, row)
			continue
		}

		newRow := discordgo.ActionsRow{}
		for _, comp := range ar.Components {
			btn, ok := comp.(discordgo.Button)
			if !ok {
				if ptr, ok2 := comp.(*discordgo.Button); ok2 && ptr != nil {
					btn = *ptr
					ok = true
				}
			}
			if ok {
				btn.Disabled = true
				newRow.Components = append(newRow.Components, btn)
			} else {
				newRow.Components = append(newRow.Components, comp)
			}
		}
		out = append(out, newRow)
	}

	return out
}

func escapeMarkdown(s string) string {
	r := strings.NewReplacer(
		"*", "\\*",
		"_", "\\_",
		"`", "\\`",
	)
	return r.Replace(s)
}

func safeThreadName(s string) string {
	s = strings.ReplaceAll(s, "#", "")
	if len(s) > 50 {
		return s[:50]
	}
	return s
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

func envBoolDefault(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
