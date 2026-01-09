package starboard

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type TopStarsModule struct {
	db      *sql.DB
	guildID string // optional, from env
}

func NewTopStars(db *sql.DB) *TopStarsModule {
	return &TopStarsModule{
		db:      db,
		guildID: strings.TrimSpace(os.Getenv("GUILD_ID")), // optional
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
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "limit",
				Description: "How many results to show (1–25, default 10)",
				Required:    false,
				MinValue:    float64Ptr(1),
				MaxValue:    25,
			},
		},
	}

	if _, err := s.ApplicationCommandCreate(appID, m.guildID, cmd); err != nil {
		log.Printf("[starboard] /topstars create failed: %v", err)
		return
	}

	log.Println("[starboard] registered /topstars")
}

func float64Ptr(v float64) *float64 { return &v }

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

func (m *TopStarsModule) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	if data.Name != "topstars" {
		return
	}

	kind := "users"
	limit := 10

	for _, opt := range data.Options {
		if opt == nil {
			continue
		}
		switch opt.Name {
		case "type":
			if v, ok := opt.Value.(string); ok && v != "" {
				kind = v
			}
		case "limit":
			limit = int(opt.IntValue())
		}
	}

	if limit < 1 {
		limit = 1
	}
	if limit > 25 {
		limit = 25
	}

	switch kind {
	case "posts":
		m.handleTopPosts(s, i, limit)
	default:
		m.handleTopUsers(s, i, limit)
	}
}

func (m *TopStarsModule) handleTopUsers(s *discordgo.Session, i *discordgo.InteractionCreate, limit int) {
	top, err := m.queryTopUsers(limit)
	if err != nil {
		respondEphemeral(s, i, "DB error reading top users.")
		return
	}
	if len(top) == 0 {
		respondEphemeral(s, i, "No starboard posts recorded yet.")
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Top Users (by starboard posts) — Top %d**\n", len(top)))
	for idx, row := range top {
		fmt.Fprintf(&b, "%d. <@%s> — **%d**\n", idx+1, row.AuthorID, row.Count)
	}
	respond(s, i, b.String())
}

func (m *TopStarsModule) handleTopPosts(s *discordgo.Session, i *discordgo.InteractionCreate, limit int) {
	top, err := m.queryTopPosts(limit)
	if err != nil {
		respondEphemeral(s, i, "DB error reading top posts.")
		return
	}
	if len(top) == 0 {
		respondEphemeral(s, i, "No starboard posts recorded yet.")
		return
	}

	guildID := i.GuildID

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Top Starboard Posts — Top %d**\n", len(top)))
	for idx, row := range top {
		jump := "(jump unavailable)"
		if guildID != "" && row.OriginalChannelID != "" && row.OriginalMessageID != "" {
			jump = makeJumpURL(guildID, row.OriginalChannelID, row.OriginalMessageID)
		}
		fmt.Fprintf(&b, "%d. ⭐ **%d** — <@%s> — %s\n", idx+1, row.StarsCount, row.AuthorID, jump)
	}
	respond(s, i, b.String())
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: msg},
	})
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

type topUserRow struct {
	AuthorID string
	Count    int
}

func (m *TopStarsModule) queryTopUsers(limit int) ([]topUserRow, error) {
	rows, err := m.db.Query(
		`SELECT author_id, COUNT(*) AS c
		 FROM starboard_posts
		 WHERE author_id != ''
		 GROUP BY author_id
		 ORDER BY c DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []topUserRow
	for rows.Next() {
		var r topUserRow
		if err := rows.Scan(&r.AuthorID, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type topPostRow struct {
	AuthorID          string
	StarsCount        int
	OriginalChannelID string
	OriginalMessageID string
}

func (m *TopStarsModule) queryTopPosts(limit int) ([]topPostRow, error) {
	rows, err := m.db.Query(
		`SELECT author_id, stars_count, original_channel_id, original_message_id
		 FROM starboard_posts
		 WHERE author_id != ''
		 ORDER BY stars_count DESC, created_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []topPostRow
	for rows.Next() {
		var r topPostRow
		if err := rows.Scan(&r.AuthorID, &r.StarsCount, &r.OriginalChannelID, &r.OriginalMessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
