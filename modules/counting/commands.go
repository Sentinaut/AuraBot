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

// Slash commands:
//  - /countingleaderboard
//  - /countinginfo (optional: type=counting|trios)

const (
	countingPageSize = 10
	countingLBPrefix = "clb" // clb:<ownerID>:<kind>:<action>:<page>
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

	guildID := strings.TrimSpace(os.Getenv("GUILD_ID")) // optional

	// Remove duplicates in current scope
	_ = deleteCommandsByName(s, appID, guildID, "countingleaderboard")
	_ = deleteCommandsByName(s, appID, guildID, "countinginfo")

	// If guild-scoped, also remove old global duplicates
	if guildID != "" {
		_ = deleteCommandsByName(s, appID, "", "countingleaderboard")
		_ = deleteCommandsByName(s, appID, "", "countinginfo")
	}

	cmds := []*discordgo.ApplicationCommand{
		{
			Name:        "countingleaderboard",
			Description: "Show the counting leaderboard (most valid counts)",
		},
		{
			Name:        "countinginfo",
			Description: "Show counting stats (current number, high score, totals)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "type",
					Description: "Which counting channel to show info for (defaults to counting)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "counting", Value: "counting"},
						{Name: "counting-trios", Value: "trios"},
					},
				},
			},
		},
	}

	// Create commands (avoid bulk overwrite so other modules keep their commands)
	for _, c := range cmds {
		if _, err := s.ApplicationCommandCreate(appID, guildID, c); err != nil {
			log.Printf("[counting] command create failed (%s): %v", c.Name, err)
			return
		}
	}

	log.Println("[counting] registered /countingleaderboard and /countinginfo")
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
		case "countingleaderboard":
			ownerID := interactionUserID(i)
			if ownerID == "" {
				respondEphemeral(s, i, "Could not determine user.")
				return
			}
			content, embed, comps, err := m.buildLeaderboardPage(ownerID, 0)
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
					Content:    content,
					Embeds:     []*discordgo.MessageEmbed{embed},
					Components: comps,
				},
			})

		case "countinginfo":
			kind := "counting"
			for _, opt := range data.Options {
				if opt == nil {
					continue
				}
				if opt.Name == "type" {
					if v, ok := opt.Value.(string); ok && v != "" {
						kind = v
					}
				}
			}

			embed, err := m.buildInfoEmbed(kind)
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
		}

	case discordgo.InteractionMessageComponent:
		m.handleLeaderboardComponent(s, i)
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
   /countingleaderboard
   ========================= */

type lbRow struct {
	UserID   string
	Username string
	Counts   int64
}

func (m *Module) fetchLeaderboardRows() ([]lbRow, error) {
	if m.db == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := m.db.Query(
		`SELECT user_id, username, counts
		 FROM counting_user_stats
		 WHERE counts > 0
		 ORDER BY counts DESC, last_counted_at DESC;`,
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Module) buildLeaderboardPage(ownerID string, page int) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	rows, err := m.fetchLeaderboardRows()
	if err != nil {
		return "", nil, nil, err
	}
	if len(rows) == 0 {
		return "", nil, nil, nil
	}

	if page < 0 {
		page = 0
	}
	maxPage := (len(rows) - 1) / countingPageSize
	if page > maxPage {
		page = maxPage
	}

	start := page * countingPageSize
	end := start + countingPageSize
	if end > len(rows) {
		end = len(rows)
	}

	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		r := rows[i]
		n := i + 1
		name := strings.TrimSpace(r.Username)
		if name == "" {
			name = "<@" + r.UserID + ">"
		}
		lines = append(lines, fmt.Sprintf("**#%d** %s — **%d**", n, name, r.Counts))
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🏆 Counting Leaderboard",
		Description: strings.Join(lines, "\n"),
		Color:       0x2ECC71,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Page %d/%d • %d users", page+1, maxPage+1, len(rows)),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	comps := leaderboardButtons(ownerID, "all", page, maxPage)
	return "", embed, comps, nil
}

func leaderboardButtons(ownerID, kind string, page, maxPage int) []discordgo.MessageComponent {
	prevDisabled := page <= 0
	nextDisabled := page >= maxPage

	custom := func(action string) string {
		return fmt.Sprintf("%s:%s:%s:%s:%d", countingLBPrefix, ownerID, kind, action, page)
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

func leaderboardLoadingButtons() []discordgo.MessageComponent {
	row := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "…", Style: discordgo.SecondaryButton, Disabled: true},
	}}
	return []discordgo.MessageComponent{row}
}

func (m *Module) handleLeaderboardComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Message == nil {
		return
	}
	data := i.MessageComponentData()

	parts := strings.Split(data.CustomID, ":")
	if len(parts) != 5 || parts[0] != countingLBPrefix {
		return
	}
	ownerID := parts[1]
	action := parts[3]
	pageStr := parts[4]

	clickerID := interactionUserID(i)
	if clickerID == "" {
		return
	}
	if clickerID != ownerID {
		respondEphemeral(s, i, "Only the person who ran this leaderboard can use these buttons.")
		return
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 0 {
		page = 0
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Components: leaderboardLoadingButtons()},
	})

	rows, err := m.fetchLeaderboardRows()
	if err != nil {
		msg := "DB error reading counting leaderboard."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &[]discordgo.MessageComponent{}})
		return
	}
	if len(rows) == 0 {
		msg := "No counting data yet."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &[]discordgo.MessageComponent{}})
		return
	}

	maxPage := (len(rows) - 1) / countingPageSize
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

	start := target * countingPageSize
	end := start + countingPageSize
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
		lines = append(lines, fmt.Sprintf("**#%d** %s — **%d**", n, name, r.Counts))
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🏆 Counting Leaderboard",
		Description: strings.Join(lines, "\n"),
		Color:       0x2ECC71,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Page %d/%d • %d users", target+1, maxPage+1, len(rows)),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	comps := leaderboardButtons(ownerID, "all", target, maxPage)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &comps,
	})
}

/* =========================
   /countinginfo
   ========================= */

type channelInfo struct {
	ChannelID    string
	LastCount    int64
	LastUserID   string
	UpdatedAt    int64
	HighScore    int64
	HighScoreAt  int64
	TotalCounted int64
}

func (m *Module) buildInfoEmbed(kind string) (*discordgo.MessageEmbed, error) {
	chID := m.countingChannelID
	if kind == "trios" {
		chID = m.triosChannelID
	}
	if strings.TrimSpace(chID) == "" {
		return &discordgo.MessageEmbed{Description: "Counting channel is not configured."}, nil
	}

	info, err := m.readChannelInfo(chID)
	if err != nil {
		return nil, err
	}

	current := fmt.Sprintf("%d", info.LastCount)

	lastBy := "Unknown"
	if info.LastUserID != "" {
		lastBy = "<@" + info.LastUserID + ">"
	}

	lastAgo := "Never"
	if info.UpdatedAt > 0 {
		lastAgo = fmt.Sprintf("<t:%d:R>", info.UpdatedAt)
	}

	high := fmt.Sprintf("%d", info.HighScore)
	highAgo := "Never"
	if info.HighScoreAt > 0 {
		highAgo = fmt.Sprintf("<t:%d:R>", info.HighScoreAt)
	}

	total := fmt.Sprintf("%d", info.TotalCounted)

	title := "ℹ️ Counting Info"
	if kind == "trios" {
		title = "ℹ️ Counting Info (Trios)"
	}

	embed := &discordgo.MessageEmbed{
		Title: title,
		Color: 0x3498DB,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Current Number", Value: current, Inline: true},
			{Name: "High Score", Value: high + " (" + highAgo + ")", Inline: true},
			{Name: "Last Counted By", Value: lastBy, Inline: true},
			{Name: "Last Counted", Value: lastAgo, Inline: true},
			{Name: "Total Numbers Counted", Value: total, Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}
	return embed, nil
}

func (m *Module) readChannelInfo(channelID string) (channelInfo, error) {
	var out channelInfo
	out.ChannelID = channelID
	if m.db == nil {
		return out, sql.ErrConnDone
	}

	_ = m.db.QueryRow(
		`SELECT last_count, last_user_id, updated_at
		 FROM counting_state
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&out.LastCount, &out.LastUserID, &out.UpdatedAt)

	_ = m.db.QueryRow(
		`SELECT high_score, high_score_at, total_counted
		 FROM counting_channel_stats
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&out.HighScore, &out.HighScoreAt, &out.TotalCounted)

	// fallback if stats row doesn't exist yet
	if out.HighScore == 0 && out.HighScoreAt == 0 && out.TotalCounted == 0 {
		out.HighScore = out.LastCount
		out.HighScoreAt = out.UpdatedAt
		out.TotalCounted = 0
	}

	return out, nil
}
