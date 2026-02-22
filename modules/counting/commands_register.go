package counting

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
		log.Println("[counting] cannot register commands: missing application ID")
		return
	}

	guildID := m.guildID

	_ = deleteCommandsByName(s, appID, guildID, "countingleaderboard")
	_ = deleteCommandsByName(s, appID, guildID, "countinginfo")
	_ = deleteCommandsByName(s, appID, guildID, "countscoreincrease")
	if guildID != "" {
		_ = deleteCommandsByName(s, appID, "", "countingleaderboard")
		_ = deleteCommandsByName(s, appID, "", "countinginfo")
		_ = deleteCommandsByName(s, appID, "", "countscoreincrease")
	}

	_, err := s.ApplicationCommandCreate(appID, guildID, &discordgo.ApplicationCommand{
		Name:        "countingleaderboard",
		Description: "Show the counting leaderboard",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "scope",
				Description: "channel (default) or total across both counting channels",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "channel", Value: "channel"},
					{Name: "total", Value: "total"},
				},
			},
		},
	})
	if err != nil {
		log.Printf("[counting] command create failed (countingleaderboard): %v", err)
		return
	}

	_, err = s.ApplicationCommandCreate(appID, guildID, &discordgo.ApplicationCommand{
		Name:        "countinginfo",
		Description: "Show counting info for the channel you run this in",
	})
	if err != nil {
		log.Printf("[counting] command create failed (countinginfo): %v", err)
		return
	}

	// /countscoreincrease user amount [channel]
	_, err = s.ApplicationCommandCreate(appID, guildID, &discordgo.ApplicationCommand{
		Name:        "countscoreincrease",
		Description: "Increase a user's counting leaderboard score",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "User to increase",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "amount",
				Description: "Amount to add (must be > 0)",
				Required:    true,
				MinValue:    func() *float64 { v := 1.0; return &v }(),
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "channel",
				Description: "Which counting channel to apply this to (optional if you run it inside one)",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "counting", Value: "counting"},
					{Name: "counting-trios", Value: "counting-trios"},
				},
			},
		},
	})
	if err != nil {
		log.Printf("[counting] command create failed (countscoreincrease): %v", err)
		return
	}

	log.Println("[counting] registered /countingleaderboard, /countinginfo, /countscoreincrease")
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
