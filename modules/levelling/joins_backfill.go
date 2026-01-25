package levelling

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) handleJoinsBackfill(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if strings.TrimSpace(i.GuildID) == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// simple admin gate: require Manage Server
	if i.Member == nil || (i.Member.Permissions&discordgo.PermissionManageServer) == 0 {
		m.respondEphemeral(s, i, "You need **Manage Server** to use this.")
		return
	}

	limit := 0
	dryRun := false

	for _, opt := range i.ApplicationCommandData().Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "limit":
			switch v := opt.Value.(type) {
			case float64:
				limit = int(v)
			case int:
				limit = v
			case string:
				if n, err := strconv.Atoi(v); err == nil {
					limit = n
				}
			}
			if limit < 0 {
				limit = 0
			}
		case "dry_run":
			if v, ok := opt.Value.(bool); ok {
				dryRun = v
			}
		}
	}

	// Immediate ACK (avoids 3s timeout)
	msg := "Backfilling join timestamps…"
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	guildID := i.GuildID

	processed := 0
	written := 0
	skippedBots := 0
	missingJoinTime := 0

	after := ""
	for {
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			out := fmt.Sprintf("Backfill failed fetching members: %v", err)
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &out})
			return
		}
		if len(members) == 0 {
			break
		}

		for _, mem := range members {
			if mem == nil || mem.User == nil || mem.User.ID == "" {
				continue
			}
			after = mem.User.ID

			if mem.User.Bot {
				skippedBots++
				continue
			}

			processed++
			if limit > 0 && processed > limit {
				goto DONE
			}

			joinedUnix := int64(0)
			// In your discordgo version JoinedAt is time.Time
			if !mem.JoinedAt.IsZero() {
				joinedUnix = mem.JoinedAt.Unix()
			} else {
				missingJoinTime++
				joinedUnix = time.Now().Unix()
			}

			if dryRun {
				written++
				continue
			}

			if err := m.upsertUserJoin(mem.User.ID, mem.User.Username, joinedUnix); err == nil {
				written++
			}
		}

		if len(members) < 1000 {
			break
		}
	}

DONE:
	out := fmt.Sprintf(
		"✅ Joins backfill complete.\n\nProcessed: **%d**\nWritten: **%d**%s\nSkipped bots: **%d**\nMissing join timestamps: **%d**",
		processed,
		written,
		func() string {
			if dryRun {
				return " (dry-run)"
			}
			return ""
		}(),
		skippedBots,
		missingJoinTime,
	)

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &out})
}
