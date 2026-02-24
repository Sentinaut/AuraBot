package welcoming

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
