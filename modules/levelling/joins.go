package levelling

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onGuildMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e == nil || e.Member == nil || e.Member.User == nil {
		return
	}
	if e.Member.User.Bot {
		return
	}

	// best-effort: treat event time as join time
	joinedAt := time.Now().Unix()
	username := e.Member.User.Username

	if err := m.upsertUserJoin(e.Member.User.ID, username, joinedAt); err != nil {
		log.Printf("[levelling] upsert join failed: %v", err)
	}
}

func (m *Module) handleJoins(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if strings.TrimSpace(i.GuildID) == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	rangeOpt := ""
	for _, opt := range i.ApplicationCommandData().Options {
		if opt != nil && opt.Name == "range" {
			if v, ok := opt.Value.(string); ok {
				rangeOpt = strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	if rangeOpt != "daily" && rangeOpt != "weekly" && rangeOpt != "monthly" {
		m.respondEphemeral(s, i, "Range must be: daily, weekly, or monthly.")
		return
	}

	loc, _ := time.LoadLocation("Europe/London")
	now := time.Now().In(loc)

	start := startOfRange(now, rangeOpt)
	end := now

	rows, err := m.listJoinsBetween(start.Unix(), end.Unix(), 50)
	if err != nil {
		m.respondEphemeral(s, i, "DB error reading joins.")
		return
	}

	title := fmt.Sprintf("Joins — %s", strings.ToUpper(rangeOpt[:1])+rangeOpt[1:])

	desc := ""
	if len(rows) == 0 {
		desc = "No joins recorded for this range yet.\n\n*Note: joins are only tracked from when this feature was added (it doesn’t backfill old members).*"
	} else {
		var b strings.Builder
		for idx, r := range rows {
			t := time.Unix(r.JoinedAt, 0).In(loc)
			fmt.Fprintf(&b, "%d. <@%s> — %s\n", idx+1, r.UserID, t.Format("02 Jan 15:04"))
		}
		desc = b.String()
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: desc,
		Color:       0x2B2D31,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("UK time • From %s", start.Format("02 Jan 2006 15:04")),
		},
		Timestamp: now.Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

func startOfRange(now time.Time, r string) time.Time {
	switch r {
	case "daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	case "weekly":
		// Monday 00:00 (UK time)
		wd := int(now.Weekday())
		// Go: Sunday=0. Convert so Monday=0..Sunday=6
		shift := (wd + 6) % 7
		d := now.AddDate(0, 0, -shift)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())
	default:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
}
