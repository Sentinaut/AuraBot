package counting

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
)

const (
	reactOK        = "✅"
	reactHighScore = "☑️"
	reactBad       = "❌"
	reactHundred   = "💯"

	emoji200  = "200:1469034517938438235"
	emoji500  = "500:1469034589505851647"
	emoji1000 = "1000:1469034633885777960"
)

type Module struct {
	db *sql.DB

	countingChannelID string
	triosChannelID    string

	ruinedRoleID string
	ruinedFor    time.Duration

	stop chan struct{}
}

func New(countingChannelID, triosChannelID, ruinedRoleID string, ruinedFor time.Duration, db *sql.DB) *Module {
	return &Module{
		db:                db,
		countingChannelID: strings.TrimSpace(countingChannelID),
		triosChannelID:    strings.TrimSpace(triosChannelID),
		ruinedRoleID:      strings.TrimSpace(ruinedRoleID),
		ruinedFor:         ruinedFor,
		stop:              make(chan struct{}),
	}
}

func (m *Module) Name() string { return "counting" }

func (m *Module) Register(s *discordgo.Session) error {
	// Slash commands are implemented in commands.go
	s.AddHandler(m.onReady)
	s.AddHandler(m.onInteractionCreate)

	// Counting message handler
	s.AddHandler(m.onMessageCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error {
	// Schema is owned by internal/db/migrate.go

	// Background expiry cleanup (role removals)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()

		// run once at startup
		m.cleanupExpired(s)

		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			case <-t.C:
				m.cleanupExpired(s)
			}
		}
	}()

	return nil
}

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil || e.Author == nil {
		return
	}
	if e.Author.Bot {
		return
	}

	mode := m.channelMode(e.ChannelID)
	if mode == modeDisabled {
		return
	}

	n, ok := parseLeadingInt(e.Content)
	if !ok {
		// Not a counting attempt; ignore.
		return
	}

	res, err := m.applyCount(mode, e.GuildID, e.ChannelID, e.Author.ID, e.Author.Username, n)
	if err != nil {
		log.Printf("[counting] apply error: %v", err)
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
		return
	}

	if res.OK {
		// ✅ normal vs ☑️ high score
		if res.HighScore {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHighScore)
		} else {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactOK)
		}

		// 💯 at 100
		if res.Count == 100 {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHundred)
		}

		// custom milestone emojis
		switch res.Count {
		case 200:
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji200)
		case 500:
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji500)
		case 1000:
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji1000)
		}

		return
	}

	_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)

	// Announce and punish
	if res.RuinedAt > 0 {
		msg := fmt.Sprintf("<@%s> **RUINED IT AT %d!!** Next number is **1**. %s",
			e.Author.ID, res.RuinedAt, res.Reason)
		_, _ = s.ChannelMessageSend(e.ChannelID, msg)
	}

	m.punish(s, e.GuildID, e.Author.ID)
}

type channelMode int

const (
	modeDisabled channelMode = iota
	modeNormal
	modeTrios
)

func (m *Module) channelMode(channelID string) channelMode {
	if m.countingChannelID != "" && channelID == m.countingChannelID {
		return modeNormal
	}
	if m.triosChannelID != "" && channelID == m.triosChannelID {
		return modeTrios
	}
	return modeDisabled
}

// parseLeadingInt returns the integer formed by the leading digits of s,
// after trimming leading whitespace. Examples:
//  "2 long until..." -> 2, true
//  "  15" -> 15, true
//  "hello 2" -> 0, false
func parseLeadingInt(s string) (int64, bool) {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s == "" {
		return 0, false
	}
	if s[0] < '0' || s[0] > '9' {
		return 0, false
	}

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

type applyResult struct {
	OK       bool
	RuinedAt int64
	Reason   string

	HighScore bool
	Count     int64
}

// applyCount enforces per-channel counting rules and persists state.
//
// If a user fails, the counter is reset to 0 so the next correct number is 1.
//
// Rules:
//  - Both modes: newCount must equal lastCount+1.
//  - Normal: same user cannot count twice in a row.
//  - Trios: user cannot count if they were one of the last TWO counters.
func (m *Module) applyCount(mode channelMode, guildID, channelID, userID, username string, newCount int64) (applyResult, error) {
	if m.db == nil {
		return applyResult{OK: false}, sql.ErrConnDone
	}

	tx, err := m.db.Begin()
	if err != nil {
		return applyResult{OK: false}, err
	}
	defer func() { _ = tx.Rollback() }()

	var lastCount int64
	var lastUser string
	var prevUser string

	err = tx.QueryRow(
		`SELECT last_count, last_user_id, prev_user_id
		 FROM counting_state
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&lastCount, &lastUser, &prevUser)

	if err != nil {
		if err == sql.ErrNoRows {
			lastCount = 0
			lastUser = ""
			prevUser = ""
		} else {
			return applyResult{OK: false}, err
		}
	}

	expected := lastCount + 1

	// Validate number
	if newCount != expected {
		if err := m.resetState(tx, channelID); err != nil {
			return applyResult{OK: false}, err
		}
		if err := tx.Commit(); err != nil {
			return applyResult{OK: false}, err
		}
		return applyResult{OK: false, RuinedAt: lastCount, Reason: "Wrong number."}, nil
	}

	// Validate spacing
	switch mode {
	case modeNormal:
		if lastUser != "" && userID == lastUser {
			if err := m.resetState(tx, channelID); err != nil {
				return applyResult{OK: false}, err
			}
			if err := tx.Commit(); err != nil {
				return applyResult{OK: false}, err
			}
			return applyResult{OK: false, RuinedAt: lastCount, Reason: "You can't count twice in a row."}, nil
		}
	case modeTrios:
		if (lastUser != "" && userID == lastUser) || (prevUser != "" && userID == prevUser) {
			if err := m.resetState(tx, channelID); err != nil {
				return applyResult{OK: false}, err
			}
			if err := tx.Commit(); err != nil {
				return applyResult{OK: false}, err
			}
			return applyResult{OK: false, RuinedAt: lastCount, Reason: "In trios you must wait for 2 other people to count."}, nil
		}
	}

	// Read previous high score (before updating)
	var prevHigh int64
	err = tx.QueryRow(
		`SELECT high_score FROM counting_channel_stats WHERE channel_id = ?;`,
		channelID,
	).Scan(&prevHigh)
	if err != nil {
		if err == sql.ErrNoRows {
			prevHigh = 0
		} else {
			return applyResult{OK: false}, err
		}
	}

	now := time.Now().Unix()

	// Success: upsert and shift history (prev <- last, last <- current)
	_, err = tx.Exec(
		`INSERT INTO counting_state (channel_id, last_count, last_user_id, prev_user_id, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
			last_count = excluded.last_count,
			prev_user_id = counting_state.last_user_id,
			last_user_id = excluded.last_user_id,
			updated_at = excluded.updated_at;`,
		channelID, newCount, userID, prevUser, now,
	)
	if err != nil {
		return applyResult{OK: false}, err
	}

	// ✅ Per-channel per-user stats (leaderboard; never goes down)
	username = strings.TrimSpace(username)
	_, err = tx.Exec(
		`INSERT INTO counting_user_stats_v2 (channel_id, user_id, username, counts, last_counted_at)
		 VALUES (?, ?, ?, 1, ?)
		 ON CONFLICT(channel_id, user_id) DO UPDATE SET
			username = CASE WHEN excluded.username != '' THEN excluded.username ELSE counting_user_stats_v2.username END,
			counts = counting_user_stats_v2.counts + 1,
			last_counted_at = excluded.last_counted_at;`,
		channelID, userID, username, now,
	)
	if err != nil {
		return applyResult{OK: false}, err
	}

	// ✅ Per-channel totals + highscore
	_, err = tx.Exec(
		`INSERT INTO counting_channel_stats (channel_id, high_score, high_score_at, total_counted)
		 VALUES (?, ?, ?, 1)
		 ON CONFLICT(channel_id) DO UPDATE SET
			total_counted = counting_channel_stats.total_counted + 1,
			high_score = CASE WHEN excluded.high_score > counting_channel_stats.high_score THEN excluded.high_score ELSE counting_channel_stats.high_score END,
			high_score_at = CASE WHEN excluded.high_score > counting_channel_stats.high_score THEN excluded.high_score_at ELSE counting_channel_stats.high_score_at END;`,
		channelID, newCount, now,
	)
	if err != nil {
		return applyResult{OK: false}, err
	}

	if err := tx.Commit(); err != nil {
		return applyResult{OK: false}, err
	}
	return applyResult{
		OK:        true,
		HighScore: newCount > prevHigh,
		Count:     newCount,
	}, nil
}

func (m *Module) resetState(tx *sql.Tx, channelID string) error {
	now := time.Now().Unix()
	_, err := tx.Exec(
		`INSERT INTO counting_state (channel_id, last_count, last_user_id, prev_user_id, updated_at)
		 VALUES (?, 0, '', '', ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
			last_count = 0,
			last_user_id = '',
			prev_user_id = '',
			updated_at = excluded.updated_at;`,
		channelID, now,
	)
	return err
}

func (m *Module) punish(s *discordgo.Session, guildID, userID string) {
	if strings.TrimSpace(guildID) == "" {
		return
	}
	if strings.TrimSpace(m.ruinedRoleID) == "" {
		return
	}
	if m.ruinedFor <= 0 {
		return
	}

	// Assign role (requires Manage Roles and role hierarchy)
	if err := s.GuildMemberRoleAdd(guildID, userID, m.ruinedRoleID); err != nil {
		log.Printf("[counting] failed to add ruined role: %v", err)
	}

	expiresAt := time.Now().Add(m.ruinedFor).Unix()
	_, err := m.db.Exec(
		`INSERT INTO counting_punishments (guild_id, user_id, role_id, expires_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(guild_id, user_id, role_id) DO UPDATE SET
			expires_at = CASE
				WHEN excluded.expires_at > counting_punishments.expires_at THEN excluded.expires_at
				ELSE counting_punishments.expires_at
			END;`,
		guildID, userID, m.ruinedRoleID, expiresAt,
	)
	if err != nil {
		log.Printf("[counting] failed to store punishment expiry: %v", err)
	}
}

func (m *Module) cleanupExpired(s *discordgo.Session) {
	if m.db == nil {
		return
	}
	if strings.TrimSpace(m.ruinedRoleID) == "" {
		return
	}

	now := time.Now().Unix()

	rows, err := m.db.Query(
		`SELECT guild_id, user_id, role_id
		 FROM counting_punishments
		 WHERE expires_at <= ?;`,
		now,
	)
	if err != nil {
		log.Printf("[counting] cleanup query error: %v", err)
		return
	}
	defer rows.Close()

	type item struct {
		guildID string
		userID  string
		roleID  string
	}

	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.guildID, &it.userID, &it.roleID); err != nil {
			log.Printf("[counting] cleanup scan error: %v", err)
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[counting] cleanup rows error: %v", err)
		return
	}

	for _, it := range items {
		if it.guildID == "" || it.userID == "" || it.roleID == "" {
			continue
		}
		if err := s.GuildMemberRoleRemove(it.guildID, it.userID, it.roleID); err != nil {
			log.Printf("[counting] failed to remove expired role (continuing): %v", err)
		}
		_, _ = m.db.Exec(
			`DELETE FROM counting_punishments WHERE guild_id = ? AND user_id = ? AND role_id = ?;`,
			it.guildID, it.userID, it.roleID,
		)
	}
}
