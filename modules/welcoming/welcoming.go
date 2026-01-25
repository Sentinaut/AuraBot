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
	memberRoleID        string

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
		sessions:            make(map[string]*onboardSession),
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

	// ───────────────── Welcome Embed (existing behaviour) ─────────────────

	if m.welcomeChannelID != "" {
		count := 0
		if g, err := s.State.Guild(e.GuildID); err == nil && g != nil {
			count = g.MemberCount
		}

		embed := &discordgo.MessageEmbed{
			Title: "👋 Welcome!",
			Description: "Welcome <@" + e.User.ID + "> to **Aura**!\n\n" +
				"React with 👋 to say hi",
			Color: 0x9B59B6,
			Footer: &discordgo.MessageEmbedFooter{
				Text: "Member #" + itoa(count),
			},
			Timestamp: time.Now().Format(time.RFC3339),
		}

		msg, err := s.ChannelMessageSendEmbed(m.welcomeChannelID, embed)
		if err == nil {
			_ = s.MessageReactionAdd(m.welcomeChannelID, msg.ID, "👋")
		}
	}

	// ───────────────── Onboarding Flow ─────────────────

	if m.onboardingChannelID == "" {
		return
	}

	parent, err := s.ChannelMessageSend(
		m.onboardingChannelID,
		"Hey <@"+e.User.ID+">\n"+
			"Welcome to Aura. Please click on the thread generated below and type in your username from in-game",
	)
	if err != nil {
		log.Printf("[welcoming] onboarding message failed: %v", err)
		return
	}

	thread, err := s.MessageThreadStartComplex(
		m.onboardingChannelID,
		parent.ID,
		&discordgo.ThreadStart{
			Name:                "Username setup - " + safeThreadName(e.User.Username),
			AutoArchiveDuration: 1440,
		},
	)
	if err != nil {
		log.Printf("[welcoming] thread creation failed: %v", err)
		return
	}

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
	if e.Author == nil || e.Author.Bot {
		return
	}

	m.mu.Lock()
	sess, ok := m.sessions[e.Author.ID]
	m.mu.Unlock()
	if !ok || e.ChannelID != sess.ThreadID {
		return
	}

	text := strings.TrimSpace(e.Content)
	if text == "" {
		return
	}

	if strings.ContainsAny(text, " \t\n\r") {
		_, _ = s.ChannelMessageSend(e.ChannelID,
			"Your username cannot contain spaces, please try again\n\nPlease type in your username below",
		)
		return
	}

	m.mu.Lock()
	sess.CandidateName = text
	m.mu.Unlock()

	embed := &discordgo.MessageEmbed{
		Description: "Your username is:\n\n**" + escapeMarkdown(text) + "**\n\nIs that correct?",
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

	_, _ = s.ChannelMessageSendComplex(e.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
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

	if i.Member == nil || i.Member.User.ID != userID {
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

	// Disable buttons
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components: disableButtons(i.Message.Components),
		},
	})

	switch action {

	case "no":
		m.mu.Lock()
		sess.CandidateName = ""
		m.mu.Unlock()

		_, _ = s.ChannelMessageSend(sess.ThreadID, "Please type in your username below")

	case "yes":
		name := sess.CandidateName
		if name == "" {
			_, _ = s.ChannelMessageSend(sess.ThreadID, "Please type in your username below")
			return
		}

		_ = s.GuildMemberNickname(sess.GuildID, sess.UserID, name)
		_ = s.GuildMemberRoleAdd(sess.GuildID, sess.UserID, m.memberRoleID)

		// Delete parent message (kills thread)
		_ = s.ChannelMessageDelete(m.onboardingChannelID, sess.ParentMsgID)

		m.mu.Lock()
		delete(m.sessions, sess.UserID)
		m.mu.Unlock()
	}
}

/* ───────────── Helpers ───────────── */

func disableButtons(rows []discordgo.MessageComponent) []discordgo.MessageComponent {
	var out []discordgo.MessageComponent
	for _, r := range rows {
		ar := discordgo.ActionsRow{}
		for _, c := range r.(*discordgo.ActionsRow).Components {
			b := c.(discordgo.Button)
			b.Disabled = true
			ar.Components = append(ar.Components, b)
		}
		out = append(out, ar)
	}
	return out
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

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"*", "\\*",
		"_", "\\_",
		"`", "\\`",
	)
	return replacer.Replace(s)
}

func safeThreadName(s string) string {
	s = strings.ReplaceAll(s, "#", "")
	if len(s) > 50 {
		return s[:50]
	}
	return s
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
