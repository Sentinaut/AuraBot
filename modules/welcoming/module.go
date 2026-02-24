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
	// Register slash commands reliably per guild
	s.AddHandler(m.onGuildCreate)

	s.AddHandler(m.onGuildMemberAdd)
	s.AddHandler(m.onGuildMemberRemove) // cleanup if they leave before verify
	s.AddHandler(m.onMessageCreate)
	s.AddHandler(m.onInteractionCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error { return nil }

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

	if _, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	}); err != nil {
		log.Printf("[welcoming] failed to send confirm message: %v", err)
	}
}

func ephemeral(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

func disableAllButtons(msg *discordgo.Message) []discordgo.MessageComponent {
	if msg == nil || len(msg.Components) == 0 {
		return nil
	}

	var out []discordgo.MessageComponent
	for _, c := range msg.Components {
		row, ok := c.(*discordgo.ActionsRow)
		if !ok {
			// discordgo may also return concrete ActionsRow
			if rr, ok2 := c.(discordgo.ActionsRow); ok2 {
				row = &rr
			}
		}
		if row == nil {
			continue
		}

		newRow := discordgo.ActionsRow{}
		for _, comp := range row.Components {
			btn, ok := comp.(*discordgo.Button)
			if !ok {
				if b2, ok2 := comp.(discordgo.Button); ok2 {
					btn = &b2
				}
			}
			if btn == nil {
				continue
			}
			b := *btn
			b.Disabled = true
			newRow.Components = append(newRow.Components, b)
		}
		out = append(out, newRow)
	}
	return out
}

func escapeMarkdown(s string) string {
	// Minimal escaping so usernames don't break embeds.
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"`", "\\`",
		"~", "\\~",
		"|", "\\|",
	)
	return replacer.Replace(s)
}

func safeThreadName(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return "onboarding"
	}

	// Discord thread name limit is 100 characters.
	if len(n) > 90 {
		n = n[:90]
	}
	return "onboarding-" + n
}

func itoa(i int) string { return strconv.Itoa(i) }

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

func deleteAfter(s *discordgo.Session, channelID, messageID string, d time.Duration) {
	if s == nil || channelID == "" || messageID == "" {
		return
	}
	go func() {
		time.Sleep(d)
		_ = s.ChannelMessageDelete(channelID, messageID)
	}()
}
