package logging

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// Module reposts selected log messages (from specific source channels) into a target channel.
//
// Filtering rules:
//   - Trade logs: message starts with "New Trade!" and usernames are extracted from the first two embeds
//     (bold username at the top of each embed, or embed title). Reposts if either username matches.
//   - Store logs + Command logs: first word of the message content is treated as the username.
//
// Matching is case-insensitive.
type Module struct {
	guildID string

	targetChannelID string

	tradeLogChannelID   string
	storeLogChannelID   string
	commandLogChannelID string

	userSet map[string]struct{}

	once sync.Once
}

func New(guildID, targetChannelID, tradeLogChannelID, storeLogChannelID, commandLogChannelID string, usernames []string) *Module {
	set := make(map[string]struct{}, len(usernames))
	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		set[strings.ToLower(u)] = struct{}{}
	}

	return &Module{
		guildID: guildID,

		targetChannelID: strings.TrimSpace(targetChannelID),

		tradeLogChannelID:   strings.TrimSpace(tradeLogChannelID),
		storeLogChannelID:   strings.TrimSpace(storeLogChannelID),
		commandLogChannelID: strings.TrimSpace(commandLogChannelID),

		userSet: set,
	}
}

func (m *Module) Name() string { return "logging" }

func (m *Module) Register(s *discordgo.Session) error {
	// Module can be registered even when disabled, but we won't add handlers.
	if m.targetChannelID == "" {
		log.Println("[logging] ChannelLogRepostTarget is empty; module disabled")
		return nil
	}
	if len(m.userSet) == 0 {
		log.Println("[logging] LogRepostUsernames is empty; module disabled")
		return nil
	}

	// Avoid double-registration if Register is called more than once.
	m.once.Do(func() {
		s.AddHandler(m.onMessageCreate)
	})

	log.Printf("[logging] enabled: reposting from trade=%s store=%s command=%s -> target=%s (usernames=%d)",
		m.tradeLogChannelID, m.storeLogChannelID, m.commandLogChannelID, m.targetChannelID, len(m.userSet),
	)
	return nil
}

func (m *Module) Start(_ context.Context, _ *discordgo.Session) error { return nil }
