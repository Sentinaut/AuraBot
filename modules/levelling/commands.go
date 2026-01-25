package levelling

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onReady(s *discordgo.Session, r *discordgo.Ready) {
	appID := ""
	if s.State != nil && s.State.User != nil {
		appID = s.State.User.ID
	}
	if appID == "" {
		log.Println("[levelling] cannot register commands: missing application ID")
		return
	}

	cmds := []*discordgo.ApplicationCommand{
		{
			Name:        "rank",
			Description: "Show a user's level and XP",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to look up (defaults to you)",
					Required:    false,
				},
			},
		},
		{
			Name:        "leaderboard",
			Description: "Show the top XP users",
		},
		{
			Name:        "joins",
			Description: "List members who joined recently",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "range",
					Description: "Time range",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "daily", Value: "daily"},
						{Name: "weekly", Value: "weekly"},
						{Name: "monthly", Value: "monthly"},
					},
				},
			},
		},

		// ✅ REQUIRED options must come first
		{
			Name:        "levelupmsg",
			Description: "Show the message that caused a user to level up",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "level",
					Description: "Level number to show (e.g. 5)",
					Required:    true,
					MinValue:    float64Ptr(1),
				},
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to look up (defaults to you)",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "visible",
					Description: "If true, makes the response visible to everyone (default: only you)",
					Required:    false,
				},
			},
		},

		{
			Name:        "levelupmsgset",
			Description: "Admin: set the message for a user's level-up (backfill older levels)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "level",
					Description: "Level number to set (e.g. 5)",
					Required:    true,
					MinValue:    float64Ptr(1),
				},
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User whose level-up message to set",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message_link",
					Description: "Discord message link (Copy Message Link)",
					Required:    true,
				},
			},
		},

		{
			Name:        "levelupmsgdelete",
			Description: "Admin: delete a saved level-up message from the database",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User whose saved level-up message you want to delete",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "level",
					Description: "Level number to delete (e.g. 5)",
					Required:    true,
					MinValue:    float64Ptr(1),
				},
			},
		},

		{
			Name:        "milestonesync",
			Description: "Admin: backfill milestone roles based on current XP (stack roles; no removals)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "dry_run",
					Description: "If true, shows what would happen without changing roles",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "limit",
					Description: "Optional: only process up to N users (0 = no limit)",
					Required:    false,
					MinValue:    float64Ptr(0),
				},
			},
		},
	}

	created, err := s.ApplicationCommandBulkOverwrite(appID, m.guildID, cmds)
	if err != nil {
		log.Printf("[levelling] bulk overwrite failed: %v", err)
		return
	}

	createdNames := map[string]struct{}{}
	for _, c := range created {
		if c != nil {
			createdNames[c.Name] = struct{}{}
		}
	}

	for _, name := range []string{
		"rank",
		"leaderboard",
		"joins",
		"levelupmsg",
		"levelupmsgset",
		"levelupmsgdelete",
		"milestonesync",
	} {
		if _, ok := createdNames[name]; ok {
			log.Printf("[levelling] registered /%s", name)
		}
	}

	// If guild-scoped, delete any old GLOBAL duplicates once
	if m.guildID != "" {
		_ = m.deleteGlobalDuplicatesOnce(s, appID, map[string]struct{}{
			"rank":             {},
			"leaderboard":      {},
			"joins":            {},
			"levelupmsg":       {},
			"levelupmsgset":    {},
			"levelupmsgdelete": {},
			"milestonesync":    {},
		})
	}
}

func float64Ptr(v float64) *float64 { return &v }

func (m *Module) deleteGlobalDuplicatesOnce(s *discordgo.Session, appID string, names map[string]struct{}) error {
	cmds, err := s.ApplicationCommands(appID, "")
	if err != nil {
		return err
	}
	for _, c := range cmds {
		if c == nil {
			continue
		}
		if _, ok := names[c.Name]; ok {
			_ = s.ApplicationCommandDelete(appID, "", c.ID)
		}
	}
	return nil
}
