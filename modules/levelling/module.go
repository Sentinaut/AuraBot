package levelling

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type memberCache struct {
	mu      sync.Mutex
	guildID string
	fetched time.Time
	ids     map[string]struct{}
}

type Module struct {
	db *sql.DB

	allowedChannels map[string]struct{}

	// XP settings
	cooldown time.Duration
	xpMin    int64
	xpMax    int64

	// Used ONLY for slash-command scope (guild vs global registration).
	// DB queries intentionally ignore guild_id (single-server design).
	guildID string

	// Milestone roles (loaded from env)
	levelRoles map[int]string

	// Cached guild member IDs (used to filter leaderboards to current members)
	members memberCache

	rngMu sync.Mutex
	rng   *rand.Rand
}

func New(channelIDs []string, db *sql.DB) *Module {
	m := &Module{
		db:              db,
		allowedChannels: make(map[string]struct{}, len(channelIDs)),
		cooldown:        2 * time.Minute,
		xpMin:           15,
		xpMax:           25,
		guildID:         strings.TrimSpace(os.Getenv("GUILD_ID")),
		levelRoles:      parseLevelRolesFromEnv(),
	}

	for _, id := range channelIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			m.allowedChannels[id] = struct{}{}
		}
	}
	return m
}

func (m *Module) Name() string { return "levelling" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onReady)
	s.AddHandler(m.onInteractionCreate)
	s.AddHandler(m.onMessageCreate)
	s.AddHandler(m.onGuildMemberAdd) // ✅ needed for join tracking
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error {
	// NOTE: DB schema (including user_joins) is handled by internal/db/migrate.go

	m.rngMu.Lock()
	m.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	m.rngMu.Unlock()
	return nil
}

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil || e.Author == nil || e.Author.Bot {
		return
	}

	// Only award XP in configured channels
	if _, ok := m.allowedChannels[e.ChannelID]; !ok {
		return
	}

	// No XP in DMs
	if strings.TrimSpace(e.GuildID) == "" {
		return
	}

	userID := e.Author.ID
	username := e.Author.Username
	now := time.Now().Unix()

	tx, err := m.db.Begin()
	if err != nil {
		log.Printf("[levelling] begin tx failed: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	curXP, lastXPAt, err := m.txGetUserXPAndLast(tx, userID)
	if err != nil {
		log.Printf("[levelling] select xp failed: %v", err)
		return
	}

	// Cooldown
	if lastXPAt != 0 {
		if time.Duration(now-lastXPAt)*time.Second < m.cooldown {
			return
		}
	}

	gain := m.randomXP()
	newXP := curXP + gain

	oldLevel := levelForXP(curXP)
	newLevel := levelForXP(newXP)

	if err := m.txUpsertUserXP(tx, userID, username, newXP, now); err != nil {
		log.Printf("[levelling] upsert xp failed: %v", err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[levelling] commit failed: %v", err)
		return
	}

	// ---- Level-up side effects (after commit) ----
	if newLevel > oldLevel {
		// Stack milestone roles
		m.applyMilestoneRoles(s, e.GuildID, userID, oldLevel, newLevel)

		content := strings.TrimSpace(e.Content)
		if content == "" {
			switch {
			case len(e.Attachments) > 0:
				content = "(no text — contains attachment(s))"
			case len(e.Embeds) > 0:
				content = "(no text — contains embed(s))"
			default:
				content = "(no text content)"
			}
		}

		// Save message that caused the level-up
		if err := m.saveLevelUpMessage(
			userID,
			username,
			newLevel,
			e.ChannelID,
			e.ID,
			content,
			now,
		); err != nil {
			log.Printf("[levelling] save level-up msg failed: %v", err)
		}

		embed := &discordgo.MessageEmbed{
			Title: "🎉 Level Up!",
			Description: fmt.Sprintf(
				"<@%s> just reached\n**Level %d**!",
				userID,
				newLevel,
			),
			Color: 0x5865F2,
			Thumbnail: &discordgo.MessageEmbedThumbnail{
				URL: e.Author.AvatarURL("128"),
			},
			Footer:    &discordgo.MessageEmbedFooter{Text: "Keep chatting to earn more XP!"},
			Timestamp: time.Now().Format(time.RFC3339),
		}

		_, _ = s.ChannelMessageSendEmbed(e.ChannelID, embed)
	}
}

func (m *Module) randomXP() int64 {
	min := m.xpMin
	max := m.xpMax
	if max < min {
		max = min
	}
	span := max - min + 1

	m.rngMu.Lock()
	defer m.rngMu.Unlock()

	if m.rng == nil {
		m.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return min + int64(m.rng.Int63n(span))
}
