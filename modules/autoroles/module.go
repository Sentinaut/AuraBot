package autoroles

import (
	"context"
	"database/sql"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type Module struct {
	db      *sql.DB
	guildID string // if set, commands register instantly for that guild (single-server design)
}

// New creates the autoroles module.
// guildID is used ONLY for slash command registration scope (guild vs global).
func New(db *sql.DB, guildID string) *Module {
	return &Module{
		db:      db,
		guildID: strings.TrimSpace(guildID),
	}
}

func (m *Module) Name() string { return "autoroles" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onReady)
	s.AddHandler(m.onInteractionCreate)
	s.AddHandler(m.onReactionAdd)

	// ✅ Clean DB mappings automatically when an autorole message is deleted
	s.AddHandler(m.onMessageDelete)

	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error { return nil }

// ---- command registration ----

func (m *Module) onReady(s *discordgo.Session, r *discordgo.Ready) {
	appID := ""
	if s.State != nil && s.State.User != nil {
		appID = s.State.User.ID
	}
	if appID == "" {
		log.Println("[autoroles] cannot register commands: missing application ID")
		return
	}

	// Always delete old versions by name to avoid duplicates.
	_ = deleteCommandsByName(s, appID, m.guildID, "autorole")
	_ = deleteCommandsByName(s, appID, m.guildID, "autoremove")
	if m.guildID != "" {
		// If we are registering guild-scoped, also delete any global versions.
		_ = deleteCommandsByName(s, appID, "", "autorole")
		_ = deleteCommandsByName(s, appID, "", "autoremove")
	}

	autoroleCmd := &discordgo.ApplicationCommand{
		Name:        "autorole",
		Description: "Create or attach a reaction-role message (channel optional; defaults to current channel)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "emoji", Description: "Emoji to react with (unicode ✅ or custom <:name:id>)", Required: true},
			{Type: discordgo.ApplicationCommandOptionRole, Name: "role", Description: "Role to toggle when a user reacts", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "text", Description: "Message text (only used when creating a new message)", Required: false},
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to post/target (defaults to current channel)", Required: false},
			{Type: discordgo.ApplicationCommandOptionString, Name: "message_id", Description: "Existing message ID (if omitted, a new message will be created)", Required: false},
		},
	}
	if _, err := s.ApplicationCommandCreate(appID, m.guildID, autoroleCmd); err != nil {
		log.Printf("[autoroles] /autorole create failed: %v", err)
	} else {
		log.Println("[autoroles] registered /autorole")
	}

	autoremoveCmd := &discordgo.ApplicationCommand{
		Name:        "autoremove",
		Description: "Remove all autorole mappings from a message",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "message_id", Description: "Message ID to remove autoroles from", Required: true},
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel the message is in (defaults to current channel)", Required: false},
		},
	}
	if _, err := s.ApplicationCommandCreate(appID, m.guildID, autoremoveCmd); err != nil {
		log.Printf("[autoroles] /autoremove create failed: %v", err)
	} else {
		log.Println("[autoroles] registered /autoremove")
	}
}

func deleteCommandsByName(s *discordgo.Session, appID, guildID, name string) error {
	cmds, err := s.ApplicationCommands(appID, guildID)
	if err != nil {
		return err
	}
	for _, c := range cmds {
		if c != nil && c.Name == name {
			_ = s.ApplicationCommandDelete(appID, guildID, c.ID)
		}
	}
	return nil
}
