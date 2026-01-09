package levelling

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) handleMilestoneSync(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := strings.TrimSpace(i.GuildID)
	if guildID == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// Admin-only: Manage Server or Administrator
	var perms int64
	if i.Member != nil {
		perms = i.Member.Permissions
	}
	if perms&(discordgo.PermissionManageGuild|discordgo.PermissionAdministrator) == 0 {
		m.respondEphemeral(s, i, "You need **Manage Server** (or Administrator) to use this.")
		return
	}

	if len(m.levelRoles) == 0 {
		m.respondEphemeral(s, i, "No milestone roles are configured. Set env vars like `LEVEL_ROLE_5=...` (or `LEVEL_ROLES=...`) and restart.")
		return
	}

	dryRun := false
	limit := 0

	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "dry_run":
			dryRun = opt.BoolValue()
		case "limit":
			limit = int(opt.IntValue())
			if limit < 0 {
				limit = 0
			}
		}
	}

	// Fast ACK (ephemeral) so Discord doesn't time out
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Running milestone sync…",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Sort milestone levels for nicer reporting
	levels := make([]int, 0, len(m.levelRoles))
	for lvl := range m.levelRoles {
		levels = append(levels, lvl)
	}
	sort.Ints(levels)

	users, err := m.listAllXPUsers(limit)
	if err != nil {
		msg := "DB error reading users."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	processed := 0
	attemptedAdds := 0
	addErrors := 0

	// crude throttling to avoid slamming rate limits in huge servers
	const batch = 25

	for idx, u := range users {
		processed++
		lvl := levelForXP(u.XP)

		for _, milestone := range levels {
			roleID := strings.TrimSpace(m.levelRoles[milestone])
			if roleID == "" {
				continue
			}
			if lvl < milestone {
				continue
			}

			attemptedAdds++
			if dryRun {
				continue
			}

			if err := s.GuildMemberRoleAdd(guildID, u.UserID, roleID); err != nil {
				addErrors++
				log.Printf("[levelling] milestonesync add failed (user=%s level=%d role=%s): %v", u.UserID, milestone, roleID, err)
			}
		}

		if !dryRun && (idx+1)%batch == 0 {
			time.Sleep(350 * time.Millisecond)
		}
	}

	mode := "APPLIED"
	if dryRun {
		mode = "DRY RUN"
	}

	summary := fmt.Sprintf(
		"✅ Milestone sync complete (%s)\nProcessed users: **%d**\nRole-add attempts: **%d**\nErrors: **%d**\nMilestones: **%v**",
		mode, processed, attemptedAdds, addErrors, levels,
	)

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &summary})
}
