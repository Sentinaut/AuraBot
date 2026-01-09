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
	lbPageSize = 10
	lbCustomID = "lb"
)

func (m *Module) handleRank(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if strings.TrimSpace(i.GuildID) == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}

	// default to invoker
	target := (*discordgo.User)(nil)
	if i.Member != nil && i.Member.User != nil {
		target = i.Member.User
	} else if i.User != nil {
		target = i.User
	}
	if target == nil {
		m.respondEphemeral(s, i, "Could not determine user.")
		return
	}

	// optional user
	for _, opt := range i.ApplicationCommandData().Options {
		if opt != nil && opt.Name == "user" {
			if u := opt.UserValue(s); u != nil {
				target = u
			}
		}
	}

	xp, err := m.getUserXP(i.GuildID, target.ID)
	if err != nil {
		m.respondEphemeral(s, i, "DB error reading XP.")
		return
	}

	level, inLevel, needNext := breakdownXP(xp)

	rankPos := int64(1)
	if pos, err := m.getRankPosition(i.GuildID, xp); err == nil && pos > 0 {
		rankPos = pos
	}

	pct := 0
	if needNext > 0 {
		pct = int((inLevel * 100) / needNext)
	}
	bar := progressBar(pct, 10)

	embed := &discordgo.MessageEmbed{
		Color:     0x5865F2,
		Timestamp: time.Now().Format(time.RFC3339),
		Author: &discordgo.MessageEmbedAuthor{
			Name:    "Rank — " + target.Username,
			IconURL: target.AvatarURL("128"),
		},
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: target.AvatarURL("256")},
		Description: fmt.Sprintf("<@%s>", target.ID),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Level", Value: fmt.Sprintf("**%d**", level), Inline: true},
			{Name: "Rank", Value: fmt.Sprintf("**#%d**", rankPos), Inline: true},
			{Name: "Total XP", Value: fmt.Sprintf("**%d**", xp), Inline: true},
			{
				Name:   "Progress to next level",
				Value:  fmt.Sprintf("%s **%d%%**\n`%d / %d XP`", bar, pct, inLevel, needNext),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "Aura • Keep chatting to earn XP"},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func progressBar(pct int, segments int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct * segments) / 100

	var b strings.Builder
	b.WriteString("`")
	for i := 0; i < segments; i++ {
		if i < filled {
			b.WriteString("▰")
		} else {
			b.WriteString("▱")
		}
	}
	b.WriteString("`")
	return b.String()
}

func (m *Module) handleLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if strings.TrimSpace(i.GuildID) == "" {
		m.respondEphemeral(s, i, "This command only works in a server.")
		return
	}
	ownerID := interactionUserID(i)
	if ownerID == "" {
		m.respondEphemeral(s, i, "Could not determine user.")
		return
	}

	content, embed, components, err := m.buildLeaderboardPage(i.GuildID, ownerID, 0)
	if err != nil {
		m.respondEphemeral(s, i, "DB error reading leaderboard.")
		return
	}
	if embed == nil {
		m.respondEphemeral(s, i, "No XP recorded yet.")
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func (m *Module) handleLeaderboardComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Message == nil {
		return
	}
	data := i.MessageComponentData()

	// expected: lb:<ownerID>:<action>:<page>
	parts := strings.Split(data.CustomID, ":")
	if len(parts) != 4 || parts[0] != lbCustomID {
		return
	}
	ownerID := parts[1]
	action := parts[2]
	pageStr := parts[3]

	clickerID := interactionUserID(i)
	if clickerID == "" {
		return
	}
	if clickerID != ownerID {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Only the person who ran this leaderboard can use these buttons.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	currentPage, _ := strconv.Atoi(pageStr)
	if currentPage < 0 {
		currentPage = 0
	}

	guildID := strings.TrimSpace(i.GuildID)
	if guildID == "" {
		guildID = strings.TrimSpace(i.Message.GuildID)
	}
	if guildID == "" {
		m.respondEphemeral(s, i, "This only works in a server.")
		return
	}

	// Fast ACK to avoid timeouts
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Components: m.loadingButtons()},
	})

	total, err := m.countXPUsers(guildID)
	if err != nil || total <= 0 {
		msg := "No XP recorded yet."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &msg,
			Components: &[]discordgo.MessageComponent{},
		})
		return
	}
	maxPage := (total - 1) / lbPageSize

	targetPage := currentPage
	switch action {
	case "top":
		targetPage = 0
	case "prev":
		targetPage = currentPage - 1
	case "next":
		targetPage = currentPage + 1
	case "last":
		targetPage = maxPage
	case "me":
		myXP, err := m.getUserXP(guildID, ownerID)
		if err == nil && myXP > 0 {
			pos, err := m.getRankPosition(guildID, myXP)
			if err == nil && pos > 0 {
				targetPage = int((pos - 1) / lbPageSize)
			}
		}
	}

	if targetPage < 0 {
		targetPage = 0
	}
	if targetPage > maxPage {
		targetPage = maxPage
	}

	content, embed, comps, err := m.buildLeaderboardPageWithTotal(guildID, ownerID, targetPage, total)
	if err != nil {
		msg := "DB error reading leaderboard."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &msg,
			Components: &[]discordgo.MessageComponent{},
		})
		return
	}

	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &comps,
	})
	if err != nil {
		log.Printf("[levelling] leaderboard edit failed: %v", err)
	}
}

func (m *Module) buildLeaderboardPage(guildID, ownerID string, page int) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	total, err := m.countXPUsers(guildID)
	if err != nil {
		return "", nil, nil, err
	}
	if total <= 0 {
		return "", nil, nil, nil
	}
	return m.buildLeaderboardPageWithTotal(guildID, ownerID, page, total)
}

func (m *Module) buildLeaderboardPageWithTotal(guildID, ownerID string, page int, total int) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	maxPage := (total - 1) / lbPageSize
	if page < 0 {
		page = 0
	}
	if page > maxPage {
		page = maxPage
	}

	offset := page * lbPageSize
	rows, err := m.queryTopXPPage(guildID, lbPageSize, offset)
	if err != nil {
		return "", nil, nil, err
	}

	startRank := offset + 1
	endRank := offset + len(rows)

	var b strings.Builder
	for idx, row := range rows {
		lvl := levelForXP(row.XP)
		fmt.Fprintf(&b, "%d. <@%s> — **Lvl %d** — **%d XP**\n", startRank+idx, row.UserID, lvl, row.XP)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "XP Leaderboard",
		Description: b.String(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Showing %d–%d of %d (Page %d/%d)", startRank, endRank, total, page+1, maxPage+1),
		},
	}

	return "", embed, m.leaderboardButtons(ownerID, page, maxPage), nil
}

func (m *Module) leaderboardButtons(ownerID string, page, maxPage int) []discordgo.MessageComponent {
	makeID := func(action string) string {
		return fmt.Sprintf("%s:%s:%s:%d", lbCustomID, ownerID, action, page)
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
			discordgo.Button{Style: discordgo.SecondaryButton, Label: "Loading…", CustomID: "lb_loading", Disabled: true},
		}},
	}
}
