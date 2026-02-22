package starboard

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type TopStarsModule struct {
	db      *sql.DB
	guildID string // used only for slash-command scope (guild vs global)
}

// NewTopStars creates the /topstars command module.
// guildID is used ONLY for slash command registration scope.
func NewTopStars(db *sql.DB, guildID string) *TopStarsModule {
	return &TopStarsModule{
		db:      db,
		guildID: strings.TrimSpace(guildID),
	}
}

func (m *TopStarsModule) Name() string { return "topstars" }

func (m *TopStarsModule) Register(s *discordgo.Session) error {
	s.AddHandler(m.onReady)
	s.AddHandler(m.onInteractionCreate)
	return nil
}

func (m *TopStarsModule) Start(ctx context.Context, s *discordgo.Session) error { return nil }

func (m *TopStarsModule) onReady(s *discordgo.Session, r *discordgo.Ready) {
	appID := ""
	if s.State != nil && s.State.User != nil {
		appID = s.State.User.ID
	}
	if appID == "" {
		log.Println("[starboard] cannot register /topstars: missing application ID")
		return
	}

	// Remove duplicates in current scope
	_ = deleteCommandsByName(s, appID, m.guildID, "topstars")

	// If registering as guild command, also remove any global duplicates (prevents doubles)
	if m.guildID != "" {
		_ = deleteCommandsByName(s, appID, "", "topstars")
	}

	cmd := &discordgo.ApplicationCommand{
		Name:        "topstars",
		Description: "Starboard leaderboards",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "type",
				Description: "Which leaderboard to show (defaults to users)",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Users (most starboard posts)", Value: "users"},
					{Name: "Posts (most stars)", Value: "posts"},
				},
			},
		},
	}

	if _, err := s.ApplicationCommandCreate(appID, m.guildID, cmd); err != nil {
		log.Printf("[starboard] /topstars create failed: %v", err)
		return
	}

	log.Println("[starboard] registered /topstars")
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

/* =========================
   Interactions
   ========================= */

func (m *TopStarsModule) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		if data.Name != "topstars" {
			return
		}

		kind := "users"
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

		ownerID := interactionUserID(i)
		if ownerID == "" {
			respondEphemeral(s, i, "Could not determine user.")
			return
		}

		content, embed, comps, err := m.buildTopStarsPage(i.GuildID, kind, ownerID, 0)
		if err != nil {
			respondEphemeral(s, i, "DB error reading topstars.")
			return
		}
		if embed == nil {
			respondEphemeral(s, i, "No starboard posts recorded yet.")
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

	case discordgo.InteractionMessageComponent:
		m.handleTopStarsComponent(s, i)
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
   UI (same buttons as /leaderboard)
   ========================= */

const (
	tsPageSize = 10
	tsCustomID = "ts"
)

func (m *TopStarsModule) handleTopStarsComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Message == nil {
		return
	}
	data := i.MessageComponentData()

	// expected: ts:<ownerID>:<kind>:<action>:<page>
	parts := strings.Split(data.CustomID, ":")
	if len(parts) != 5 || parts[0] != tsCustomID {
		return
	}
	ownerID := parts[1]
	kind := parts[2]
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
				Content: "Only the person who ran this leaderboard can use these buttons.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 0 {
		page = 0
	}

	// Fast ACK to avoid timeouts
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Components: topstarsLoadingButtons()},
	})

	users, posts, note, err := m.getTopStarsRows(i.GuildID, kind)
	if err != nil {
		msg := "DB error reading topstars."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &msg,
			Components: &[]discordgo.MessageComponent{},
		})
		return
	}

	total := 0
	if kind == "posts" {
		total = len(posts)
	} else {
		total = len(users)
	}
	if total == 0 {
		msg := "No starboard posts recorded yet."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &msg,
			Components: &[]discordgo.MessageComponent{},
		})
		return
	}

	maxPage := (total - 1) / tsPageSize

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
		// jump to the page containing the invoker
		if kind == "posts" {
			for idx, r := range posts {
				if r.AuthorID == ownerID {
					targetPage = idx / tsPageSize
					break
				}
			}
		} else {
			for idx, r := range users {
				if r.AuthorID == ownerID {
					targetPage = idx / tsPageSize
					break
				}
			}
		}
	}

	if targetPage < 0 {
		targetPage = 0
	}
	if targetPage > maxPage {
		targetPage = maxPage
	}

	content, embed, comps := m.buildTopStarsPageFromRows(i.GuildID, kind, ownerID, targetPage, users, posts, note)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &comps,
	})
}

func topstarsLoadingButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Style: discordgo.SecondaryButton, Label: "Loading…", CustomID: "ts_loading", Disabled: true},
		}},
	}
}

func topstarsButtons(ownerID, kind string, page, maxPage int) []discordgo.MessageComponent {
	makeID := func(action string) string {
		return fmt.Sprintf("%s:%s:%s:%s:%d", tsCustomID, ownerID, kind, action, page)
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

/* =========================
   Data + Page Builder
   ========================= */

type topUserRow struct {
	AuthorID string
	Count    int
}

type topPostRow struct {
	AuthorID          string
	StarsCount        int
	OriginalChannelID string
	OriginalMessageID string
}

func (m *TopStarsModule) buildTopStarsPage(guildID, kind, ownerID string, page int) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	users, posts, note, err := m.getTopStarsRows(guildID, kind)
	if err != nil {
		return "", nil, nil, err
	}
	if kind == "posts" && len(posts) == 0 {
		return "", nil, nil, nil
	}
	if kind != "posts" && len(users) == 0 {
		return "", nil, nil, nil
	}

	// ✅ FIX: unpack the 3 return values
	content, embed, comps := m.buildTopStarsPageFromRows(guildID, kind, ownerID, page, users, posts, note)
	return content, embed, comps, nil
}

func (m *TopStarsModule) buildTopStarsPageFromRows(guildID, kind, ownerID string, page int, users []topUserRow, posts []topPostRow, note string) (string, *discordgo.MessageEmbed, []discordgo.MessageComponent) {
	total := 0
	if kind == "posts" {
		total = len(posts)
	} else {
		total = len(users)
	}
	maxPage := (total - 1) / tsPageSize
	if page < 0 {
		page = 0
	}
	if page > maxPage {
		page = maxPage
	}

	offset := page * tsPageSize
	end := offset + tsPageSize
	if end > total {
		end = total
	}
	startRank := offset + 1
	endRank := end

	var b strings.Builder

	title := ""
	if kind == "posts" {
		title = "Top Starboard Posts"
		for idx := offset; idx < end; idx++ {
			row := posts[idx]
			jump := "(jump unavailable)"
			if strings.TrimSpace(guildID) != "" && row.OriginalChannelID != "" && row.OriginalMessageID != "" {
				jump = makeJumpURL(guildID, row.OriginalChannelID, row.OriginalMessageID)
			}
			fmt.Fprintf(&b, "%d. ⭐ **%d** — <@%s> — %s\n", startRank+(idx-offset), row.StarsCount, row.AuthorID, jump)
		}
	} else {
		title = "Top Users (by starboard posts)"
		for idx := offset; idx < end; idx++ {
			row := users[idx]
			fmt.Fprintf(&b, "%d. <@%s> — **%d**\n", startRank+(idx-offset), row.AuthorID, row.Count)
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: b.String(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Showing %d–%d of %d (Page %d/%d)", startRank, endRank, total, page+1, maxPage+1),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	content := strings.TrimSpace(note)
	comps := topstarsButtons(ownerID, kind, page, maxPage)
	return content, embed, comps
}

func (m *TopStarsModule) getTopStarsRows(guildID, kind string) ([]topUserRow, []topPostRow, string, error) {
	_ = guildID // not stored in DB; jump URLs use interaction guildID

	if kind == "posts" {
		posts, err := m.queryAllTopPosts()
		return nil, posts, "", err
	}
	users, err := m.queryAllTopUsers()
	return users, nil, "", err
}

func (m *TopStarsModule) queryAllTopUsers() ([]topUserRow, error) {
	rows, err := m.db.Query(
		`SELECT author_id, COUNT(*) AS c
		 FROM starboard_posts
		 WHERE author_id != ''
		 GROUP BY author_id
		 ORDER BY c DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]topUserRow, 0, 128)
	for rows.Next() {
		var r topUserRow
		if err := rows.Scan(&r.AuthorID, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *TopStarsModule) queryAllTopPosts() ([]topPostRow, error) {
	rows, err := m.db.Query(
		`SELECT author_id, stars_count, original_channel_id, original_message_id
		 FROM starboard_posts
		 WHERE author_id != ''
		 ORDER BY stars_count DESC, created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]topPostRow, 0, 256)
	for rows.Next() {
		var r topPostRow
		if err := rows.Scan(&r.AuthorID, &r.StarsCount, &r.OriginalChannelID, &r.OriginalMessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
