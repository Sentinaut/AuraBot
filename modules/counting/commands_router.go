package counting

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		switch data.Name {

		case "countinginfo":
			chMode := m.channelMode(i.ChannelID)
			if chMode == modeDisabled {
				respondEphemeral(s, i, "This command can only be used in #counting or #counting-trios.")
				return
			}

			// Determine server name for title
			serverName := "Server"
			if i.GuildID != "" {
				if s.State != nil {
					if g, err := s.State.Guild(i.GuildID); err == nil && g != nil && strings.TrimSpace(g.Name) != "" {
						serverName = g.Name
					}
				}
				if serverName == "Server" {
					if g, err := s.Guild(i.GuildID); err == nil && g != nil && strings.TrimSpace(g.Name) != "" {
						serverName = g.Name
					}
				}
			}

			embed, err := m.buildCountingInfoEmbed(i.ChannelID, serverName)
			if err != nil {
				respondEphemeral(s, i, "DB error reading counting info.")
				return
			}

			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Embeds: []*discordgo.MessageEmbed{embed},
				},
			})

		case "countingleaderboard":
			scope := "channel"
			for _, opt := range data.Options {
				if opt != nil && opt.Name == "scope" {
					if v, ok := opt.Value.(string); ok && v != "" {
						scope = v
					}
				}
			}

			ownerID := interactionUserID(i)
			if ownerID == "" {
				respondEphemeral(s, i, "Could not determine user.")
				return
			}

			// Channel scope requires you run it in a counting channel
			if scope == "channel" {
				if m.channelMode(i.ChannelID) == modeDisabled {
					respondEphemeral(s, i, "Run this in #counting or #counting-trios, or use scope: total.")
					return
				}
			}

			embed, comps, err := m.buildLeaderboardEmbed(ownerID, scope, i.ChannelID, 0)
			if err != nil {
				respondEphemeral(s, i, "DB error reading counting leaderboard.")
				return
			}
			if embed == nil {
				respondEphemeral(s, i, "No counting data yet.")
				return
			}

			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Embeds:     []*discordgo.MessageEmbed{embed},
					Components: comps,
				},
			})

		case "countscoreincrease":
			// Require Manage Guild (Manage Server)
			if i.Member == nil || (i.Member.Permissions&discordgo.PermissionManageGuild) == 0 {
				respondEphemeral(s, i, "You need **Manage Server** to use this.")
				return
			}

			targetUserID := ""
			amount := int64(0)
			channelChoice := "" // "counting" | "counting-trios" | ""

			for _, opt := range data.Options {
				if opt == nil {
					continue
				}
				switch opt.Name {
				case "user":
					// Older discordgo: user option value is typically the user ID as a string
					if v, ok := opt.Value.(string); ok && v != "" {
						targetUserID = v
					}
				case "amount":
					switch v := opt.Value.(type) {
					case int64:
						amount = v
					case float64:
						amount = int64(v)
					case string:
						parsed, _ := strconv.ParseInt(v, 10, 64)
						amount = parsed
					}
				case "channel":
					if v, ok := opt.Value.(string); ok && v != "" {
						channelChoice = v
					}
				}
			}

			if targetUserID == "" {
				respondEphemeral(s, i, "Missing user.")
				return
			}
			if amount <= 0 {
				respondEphemeral(s, i, "Amount must be **greater than 0**.")
				return
			}

			// Decide which channel leaderboard to apply to:
			// - If channel option provided: use that
			// - Else: use current channel if it's a counting channel
			targetChannelID := ""
			switch channelChoice {
			case "counting":
				targetChannelID = m.countingChannelID
			case "counting-trios":
				targetChannelID = m.triosChannelID
			case "":
				// fallback to current channel if it's a counting channel
				if m.channelMode(i.ChannelID) != modeDisabled {
					targetChannelID = i.ChannelID
				}
			default:
				// should never happen because choices restrict it, but be safe
				targetChannelID = ""
			}

			if strings.TrimSpace(targetChannelID) == "" {
				respondEphemeral(s, i, "Pick a channel option (counting / counting-trios), or run the command inside one of the counting channels.")
				return
			}

			// Best-effort username
			username := ""
			if data.Resolved != nil && data.Resolved.Users != nil {
				if u, ok := data.Resolved.Users[targetUserID]; ok && u != nil {
					username = u.Username
				}
			}

			if err := m.increaseCountScore(targetChannelID, targetUserID, username, amount); err != nil {
				log.Printf("[counting] countscoreincrease db error: %v", err)
				respondEphemeral(s, i, "DB error updating score.")
				return
			}

			which := "this channel"
			if targetChannelID == m.countingChannelID {
				which = "#counting"
			} else if targetChannelID == m.triosChannelID {
				which = "#counting-trios"
			}

			respondEphemeral(s, i, fmt.Sprintf("Added **%d** to <@%s> in %s’s counting leaderboard.", amount, targetUserID, which))
		}

	case discordgo.InteractionMessageComponent:
		m.handleLeaderboardButtons(s, i)
	}
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
