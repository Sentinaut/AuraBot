package welcoming

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Module struct {
	welcomeChannelID    string
	onboardingChannelID string
	memberRoleID        string // granted AFTER username confirmed

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

func New(welcomeChannelID, onboardingChannelID, memberRoleID string) *Module {
	return &Module{
		welcomeChannelID:    strings.TrimSpace(welcomeChannelID),
		onboardingChannelID: strings.TrimSpace(onboardingChannelID),
		memberRoleID:        strings.TrimSpace(memberRoleID),

		// Hard-set join roles:
		unverifiedRoleID: "1465371447558934528",
		joinRoleID:       "1471590620673085593",

		sessions: make(map[string]*onboardSession),
	}
}

func (m *Module) Name() string { return "welcoming" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onGuildMemberAdd)
	s.AddHandler(m.onMessageCreate)
	s.AddHandler(m.onInteractionCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error { return nil }

func (m *Module) onGuildMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e == nil || e.User == nil {
		return
	}

	// ───────────── Give roles immediately on join ─────────────
	if m.unverifiedRoleID != "" {
		if err := s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.unverifiedRoleID); err != nil {
			log.Printf("[welcoming] failed to add unverified role: %v", err)
		}
	}
	if m.joinRoleID != "" {
		if err := s.GuildMemberRoleAdd(e.GuildID, e.User.ID, m.joinRoleID); err != nil {
			log.Printf("[welcoming] failed to add join role: %v", err)
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

		msg, err := s.ChannelMessageSendEmbed(m.welcomeChannelID, embed)
		if err != nil {
			log.Printf("[welcoming] failed to send welcome message: %v", err)
		} else {
			if err := s.MessageReactionAdd(m.welcomeChannelID, msg.ID, "👋"); err != nil {
				log.Printf("[welcoming] failed to add wave reaction: %v", err)
			}
		}
	}

	// ───────────── Onboarding channel flow ─────────────
	if m.onboardingChannelID == "" {
		return
	}

	// Create parent message in onboarding channel
	parent, err := s.ChannelMessageSend(
		m.onboardingChannelID,
		"Hey <@"+e.User.ID+">\n"+
			"Welcome to Aura. Please click on the thread generated below and type in your username from in-game",
	)
	if err != nil {
		log.Printf("[welcoming] onboarding message failed: %v", err)
		return
	}

	// Create thread for that message
	thread, err := s.MessageThreadStartComplex(
		m.onboardingChannelID,
		parent.ID,
		&discordgo.ThreadStart{
			Name:                "Username setup - " + safeThreadName(e.User.Username),
			AutoArchiveDuration: 1440,
		},
	)
	if err != nil {
		log.Printf("[welcoming] onboarding thread failed: %v", err)
		return
	}

	// Store session
	m.mu.Lock()
	m.sessions[e.User.ID] = &onboardSession{
		GuildID:     e.GuildID,
		UserID:      e.User.ID,
		ParentMsgID: parent.ID,
		ThreadID:    thread.ID,
	}
	m.mu.Unlock()

	_, _ = s.ChannelMessageSend(thread.ID, "Please type in your username below (no spaces).")
}

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Author == nil || e.Author.Bot {
		return
	}

	m.mu.Lock()
	sess, ok := m.sessions[e.Author.ID]
	m.mu.Unlock()
	if !ok || sess == nil {
		return
	}

	// Only accept messages in the correct thread
	if e.ChannelID != sess.ThreadID {
		return
	}

	text := strings.TrimSpace(e.Content)
	if text == "" {
		return
	}

	// Any whitespace (space/tab/newline) is invalid
	if strings.ContainsAny(text, " \t\r\n") {
		_, _ = s.ChannelMessageSend(e.ChannelID, "Your username cannot contain spaces, please try again")
		_, _ = s.ChannelMessageSend(e.ChannelID, "Please type in your username below")
		return
	}

	// Save candidate name
	m.mu.Lock()
	sess.CandidateName = text
	m.mu.Unlock()

	// Confirmation embed + buttons
	embed := &discordgo.MessageEmbed{
		Description: "Your username is:\n**" + escapeMarkdown(text) + "**\n\nIs that correct?",
		Color:       0x9B59B6,
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Yes",
					Style:    discordgo.SuccessButton,
					CustomID: "welcoming:yes:" + sess.UserID,
				},
				discordgo.Button{
					Label:    "No",
					Style:    discordgo.DangerButton,
					CustomID: "welcoming:no:" + sess.UserID,
				},
			},
		},
	}

	_, err := s.ChannelMessageSendComplex(e.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
		AllowedMentions: &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{}, // ✅ FIXED TYPE
		},
	})
	if err != nil {
		log.Printf("[welcoming] failed to send confirm embed: %v", err)
	}
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

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

		// Give member role (whatever you pass into New / env config)
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

		// Delete the onboarding thread (and then the parent message).
		// Note: deleting the parent message does NOT reliably remove the thread in Discord,
		// so we explicitly delete the thread channel.
		if _, err := s.ChannelDelete(sess.ThreadID); err != nil {
			log.Printf("[welcoming] failed to delete onboarding thread: %v", err)

			// Fallback: archive + lock the thread so it disappears from active threads.
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
		delete(m.sessions, sess.UserID)
		m.mu.Unlock()
	}
}

/* ───────────── Helpers ───────────── */

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
