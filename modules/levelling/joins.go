package levelling

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	jnPageSize = 10
	jnCustomID = "jn"
)

func (m *Module) onGuildMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e == nil || e.Member == nil || e.Member.User == nil {
		return
	}
	if e.Member.User.Bot {
		return
	}

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

	ownerID := interactionUserID(i)
	if ownerID == "" {
		m.respondEphemeral(s, i, "Could not determine user.")
		return
	}

	content, embed, comps, err := m.buildJoinsPage(rangeOpt, ownerID, 0)
	if err != nil {
		m.respondEphemeral(s, i, "DB error reading joins.")
		return
	}
	if embed == nil {
		m.respondEphemeral(s, i, "No joins recorded for this range yet.")
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
}

func (m *Module) handleJoinsComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Message == nil {
		return
	}
	data := i.MessageComponentData()

	// expected: jn:<ownerID>:<range>:<action>:<page>
	parts := strings.Split(data.CustomID, ":")
	if len(parts) != 5 || parts[0] != jnCustomID {
		return
	}
	ownerID := parts[1]
	rangeOpt := parts[2]
	action := parts[3]
	pageStr := parts[4]

	clickerID := interactionUserID(i)
	if clickerID == "" {
		return
	}
	if clickerID != ownerID {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Only the person who ran this command can use these buttons.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 0 {
		page = 0
	}

	// Fast ACK
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Components: m.loadingButtons()},
	})

	// Load all rows for this range (cap to 1000 so the bot can't be forced into huge responses)
	rows, note, err := m.getJoinsRows(rangeOpt, 1000)
	if err != nil {
		msg := "DB error reading joins."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &msg,
			Components: &[]discordgo.MessageComponent{},
		})
		return
	}
	if len(rows) == 0 {
		msg := "No joins recorded for this range yet."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &msg,
			Components: &[]discordgo.MessageComponent{},
		})
		return
	}

	total := len(rows)
	maxPage := (total - 1) / jnPageSize

	targetPage := page
	switch action {
	case "top":
		targetPage = 0
	case "prev":
		targetPage = page - 1
	case "next":
		targetPage = page + 1
	case "last":
		targetPage = maxPage
	case "me":
		// Jump to the page where the invoker appears (if present)
		for idx, r := range rows {
			if r.UserID == ownerID {
				targetPage = idx / jnPageSize
				break
			}
		}
	}

	if targetPage < 0 {
		targetPage = 0
	}
	if targetPage > maxPage {
		targetPage = maxPage
	}

	content, embed, comps := buildJoinsPageFromRows(rangeOpt, ownerID, targetPage, rows, note)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &comps,
	})
}

func (m *Module) buildJoinsPage(rangeOpt, ownerID string, page int) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	rows, note, err := m.getJoinsRows(rangeOpt, 1000)
	if err != nil {
		return "", nil, nil, err
	}
	if len(rows) == 0 {
		return "", nil, nil, nil
	}
	content, embed, comps := buildJoinsPageFromRows(rangeOpt, ownerID, page, rows, note)
	return content, embed, comps, nil
}

func (m *Module) getJoinsRows(rangeOpt string, capLimit int) ([]joinRow, string, error) {
	loc, _ := time.LoadLocation("Europe/London")
	now := time.Now().In(loc)

	start := startOfRange(now, rangeOpt)
	end := now

	rows, err := m.listJoinsBetween(start.Unix(), end.Unix(), capLimit)
	if err != nil {
		return nil, "", err
	}

	note := ""
	if len(rows) == capLimit {
		note = "⚠️ Showing the most recent joins only (internal cap reached)."
	}
	return rows, note, nil
}

func buildJoinsPageFromRows(rangeOpt, ownerID string, page int, rows []joinRow, note string) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent) {
	total := len(rows)
	maxPage := (total - 1) / jnPageSize
	if page < 0 {
		page = 0
	}
	if page > maxPage {
		page = maxPage
	}

	offset := page * jnPageSize
	end := offset + jnPageSize
	if end > total {
		end = total
	}

	startRank := offset + 1
	endRank := end

	loc, _ := time.LoadLocation("Europe/London")

	var b strings.Builder
	for idx := offset; idx < end; idx++ {
		r := rows[idx]
		t := time.Unix(r.JoinedAt, 0).In(loc)
		fmt.Fprintf(&b, "%d. <@%s> — %s\n", startRank+(idx-offset), r.UserID, t.Format("02 Jan 15:04"))
	}

	title := fmt.Sprintf("Joins — %s", strings.ToUpper(rangeOpt[:1])+rangeOpt[1:])

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: b.String(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Showing %d–%d of %d (Page %d/%d)", startRank, endRank, total, page+1, maxPage+1),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	content := strings.TrimSpace(note)
	comps := joinsButtons(ownerID, rangeOpt, page, maxPage)
	return content, embed, comps
}

func joinsButtons(ownerID, rangeOpt string, page, maxPage int) []discordgo.MessageComponent {
	makeID := func(action string) string {
		return fmt.Sprintf("%s:%s:%s:%s:%d", jnCustomID, ownerID, rangeOpt, action, page)
	}
	row := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{Style: discordgo.PrimaryButton, Label: "⏮️", CustomID: makeID("top"), Disabled: page == 0},
			discordgo.Button{Style: discordgo.SecondaryButton, Label: "⬅️", CustomID: makeID("prev"), Disabled: page == 0},
			discordgo.Button{Style: discordgo.SecondaryButton, Label: "🚹", CustomID: makeID("me")},
			discordgo.Button{Style: discordgo.SecondaryButton, Label: "➡️", CustomID: makeID("next"), Disabled: page >= maxPage},
			discordgo.Button{Style: discordgo.PrimaryButton, Label: "⏭️", CustomID: makeID("last"), Disabled: page >= maxPage},
		},
	}
	return []discordgo.MessageComponent{row}
}

func (m *Module) loadingButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Style: discordgo.SecondaryButton, Label: "Loading…", CustomID: "jn_loading", Disabled: true},
		}},
	}
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
		shift := (wd + 6) % 7 // Monday=0 ... Sunday=6
		d := now.AddDate(0, 0, -shift)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())
	default:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
}
