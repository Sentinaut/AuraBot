package counting

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	pageSize     = 10
	lbCustomBase = "clb" // clb:<ownerID>:<scope>:<page>:<action>
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

	guildID := strings.TrimSpace(os.Getenv("GUILD_ID")) // optional, like starboard

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

	// NEW: /countscoreincrease user amount [channel]
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

/* =========================
   COUNTING INFO (per channel)
   ========================= */

func (m *Module) buildCountingInfoEmbed(channelID string, serverName string) (*discordgo.MessageEmbed, error) {
	if m.db == nil {
		return nil, sql.ErrConnDone
	}

	var lastCount int64
	var lastUserID string
	var updatedAt int64
	_ = m.db.QueryRow(
		`SELECT last_count, last_user_id, updated_at
		 FROM counting_state
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&lastCount, &lastUserID, &updatedAt)

	var highScore int64
	var highAt int64
	var total int64
	_ = m.db.QueryRow(
		`SELECT high_score, high_score_at, total_counted
		 FROM counting_channel_stats
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&highScore, &highAt, &total)

	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		serverName = "Server"
	}

	// Title rules:
	// - normal: {servername} (Standard)
	// - trios:  {servername} (Trios)
	title := fmt.Sprintf("%s (Standard)", serverName)
	if channelID == m.triosChannelID {
		title = fmt.Sprintf("%s (Trios)", serverName)
	}

	lastBy := "Unknown"
	if lastUserID != "" {
		lastBy = "<@" + lastUserID + ">"
	}

	lastAgo := "Never"
	if updatedAt > 0 {
		lastAgo = fmt.Sprintf("<t:%d:R>", updatedAt)
	}

	highAgo := "Never"
	if highAt > 0 {
		highAgo = fmt.Sprintf("<t:%d:R>", highAt)
	}

	embed := &discordgo.MessageEmbed{
		Title: title,
		Color: 0x5865F2,
		Description: fmt.Sprintf(
			"**Current Number:** %d\n"+
				"**High Score:** %d (%s)\n"+
				"**Total Counted:** %d\n"+
				"**Last counted by:** %s\n"+
				"**Last count:** %s",
			lastCount,
			highScore,
			highAgo,
			total,
			lastBy,
			lastAgo,
		),
		Timestamp: time.Now().Format(time.RFC3339),
	}

	return embed, nil
}

/* =========================
   SCORE INCREASE (manual import)
   ========================= */

func (m *Module) increaseCountScore(channelID, userID, username string, amount int64) error {
	if m.db == nil {
		return sql.ErrConnDone
	}

	username = strings.TrimSpace(username)
	now := time.Now().Unix()

	_, err := m.db.Exec(
		`INSERT INTO counting_user_stats_v2 (channel_id, user_id, username, counts, last_counted_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id, user_id) DO UPDATE SET
			username = CASE WHEN excluded.username != '' THEN excluded.username ELSE counting_user_stats_v2.username END,
			counts = counting_user_stats_v2.counts + excluded.counts,
			last_counted_at = excluded.last_counted_at;`,
		channelID, userID, username, amount, now,
	)
	return err
}

/* =========================
   LEADERBOARD
   scope=channel: per channel you run it in
   scope=total: combined across both channels
   ========================= */

type lbRow struct {
	UserID   string
	Username string
	Counts   int64
}

func (m *Module) buildLeaderboardEmbed(ownerID, scope, channelID string, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	rows, err := m.fetchLeaderboard(scope, channelID)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}

	if page < 0 {
		page = 0
	}
	maxPage := (len(rows) - 1) / pageSize
	if page > maxPage {
		page = maxPage
	}

	start := page * pageSize
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}

	lines := make([]string, 0, end-start)
	for idx := start; idx < end; idx++ {
		r := rows[idx]
		n := idx + 1
		name := strings.TrimSpace(r.Username)
		if name == "" {
			name = "<@" + r.UserID + ">"
		}
		lines = append(lines, fmt.Sprintf("**#%d** %s, **%d**", n, name, r.Counts))
	}

	title := "TOP USERS IN PlayAura 🌻"
	if scope == "channel" && channelID == m.triosChannelID {
		title = "TOP USERS IN PlayAura (Trios) 🌻"
	}
	if scope == "total" {
		title = "TOP USERS IN PlayAura (Total) 🌻"
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: strings.Join(lines, "\n"),
		Color:       0x2ECC71,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	comps := leaderboardButtons(ownerID, scope, page, maxPage)
	return embed, comps, nil
}

func (m *Module) fetchLeaderboard(scope, channelID string) ([]lbRow, error) {
	if m.db == nil {
		return nil, sql.ErrConnDone
	}

	if scope == "total" {
		rows, err := m.db.Query(
			`SELECT user_id,
			        MAX(username) AS username,
			        SUM(counts) AS counts
			 FROM counting_user_stats_v2
			 WHERE channel_id IN (?, ?)
			 GROUP BY user_id
			 HAVING SUM(counts) > 0
			 ORDER BY SUM(counts) DESC;`,
			m.countingChannelID, m.triosChannelID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var out []lbRow
		for rows.Next() {
			var r lbRow
			if err := rows.Scan(&r.UserID, &r.Username, &r.Counts); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	rows, err := m.db.Query(
		`SELECT user_id, username, counts
		 FROM counting_user_stats_v2
		 WHERE channel_id = ? AND counts > 0
		 ORDER BY counts DESC, last_counted_at DESC;`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []lbRow
	for rows.Next() {
		var r lbRow
		if err := rows.Scan(&r.UserID, &r.Username, &r.Counts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func leaderboardButtons(ownerID, scope string, page, maxPage int) []discordgo.MessageComponent {
	prevDisabled := page <= 0
	nextDisabled := page >= maxPage

	custom := func(action string) string {
		return fmt.Sprintf("%s:%s:%s:%d:%s", lbCustomBase, ownerID, scope, page, action)
	}

	row := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "⏮", Style: discordgo.SecondaryButton, CustomID: custom("top"), Disabled: prevDisabled},
		discordgo.Button{Label: "◀", Style: discordgo.SecondaryButton, CustomID: custom("prev"), Disabled: prevDisabled},
		discordgo.Button{Label: "▶", Style: discordgo.SecondaryButton, CustomID: custom("next"), Disabled: nextDisabled},
		discordgo.Button{Label: "⏭", Style: discordgo.SecondaryButton, CustomID: custom("end"), Disabled: nextDisabled},
		discordgo.Button{Label: "🔄", Style: discordgo.PrimaryButton, CustomID: custom("refresh")},
	}}

	return []discordgo.MessageComponent{row}
}

func (m *Module) handleLeaderboardButtons(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Message == nil {
		return
	}

	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 5 || parts[0] != lbCustomBase {
		return
	}

	ownerID := parts[1]
	scope := parts[2]
	pageStr := parts[3]
	action := parts[4]

	clicker := interactionUserID(i)
	if clicker == "" || clicker != ownerID {
		respondEphemeral(s, i, "Only the person who ran this leaderboard can use these buttons.")
		return
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 0 {
		page = 0
	}

	channelID := i.ChannelID

	rows, err := m.fetchLeaderboard(scope, channelID)
	if err != nil || len(rows) == 0 {
		msg := "No counting data yet."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	maxPage := (len(rows) - 1) / pageSize
	target := page

	switch action {
	case "top":
		target = 0
	case "prev":
		target = page - 1
	case "next":
		target = page + 1
	case "end":
		target = maxPage
	case "refresh":
		target = page
	}

	if target < 0 {
		target = 0
	}
	if target > maxPage {
		target = maxPage
	}

	embed, comps, err := m.buildLeaderboardEmbed(ownerID, scope, channelID, target)
	if err != nil || embed == nil {
		msg := "DB error reading counting leaderboard."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: comps,
		},
	})
}
