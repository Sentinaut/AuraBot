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
	reactHighScore = "☑️" // blue tick for highscore
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
	s.AddHandler(m.onReady)
	s.AddHandler(m.onInteractionCreate)
	s.AddHandler(m.onMessageCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()

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
		return
	}

	res, err := m.applyCount(mode, e.GuildID, e.ChannelID, e.Author.ID, e.Author.Username, n)
	if err != nil {
		log.Printf("[counting] apply error: %v", err)
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
		return
	}

	if res.OK {
		// normal vs highscore tick
		if res.HighScore {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHighScore)
		} else {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactOK)
		}

		// 100
		if res.Hit100 {
			_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHundred)
		}

		// custom milestones
		switch n {
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

	if res.RuinedAt > 0 {
		msg := fmt.Sprintf(
			"<@%s> **RUINED IT AT %d!!** Next number is **1**. %s",
			e.Author.ID,
			res.RuinedAt,
			res.Reason,
		)
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

func parseLeadingInt(s string) (int64, bool) {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, false
	}

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	return n, err == nil
}

type applyResult struct {
	OK        bool
	RuinedAt int64
	Reason   string

	HighScore bool
	Hit100    bool
}

func (m *Module) applyCount(mode channelMode, guildID, channelID, userID, username string, newCount int64) (applyResult, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return applyResult{}, err
	}
	defer tx.Rollback()

	var lastCount int64
	var lastUser, prevUser string

	err = tx.QueryRow(
		`SELECT last_count, last_user_id, prev_user_id
		 FROM counting_state
		 WHERE channel_id = ?;`,
		channelID,
	).Scan(&lastCount, &lastUser, &prevUser)

	if err == sql.ErrNoRows {
		lastCount = 0
		lastUser = ""
		prevUser = ""
	} else if err != nil {
		return applyResult{}, err
	}

	expected := lastCount + 1
	if newCount != expected {
		_ = m.resetState(tx, channelID)
		_ = tx.Commit()
		return applyResult{OK: false, RuinedAt: lastCount, Reason: "Wrong number."}, nil
	}

	if mode == modeNormal && userID == lastUser {
		_ = m.resetState(tx, channelID)
		_ = tx.Commit()
		return applyResult{OK: false, RuinedAt: lastCount, Reason: "You can't count twice in a row."}, nil
	}

	if mode == modeTrios && (userID == lastUser || userID == prevUser) {
		_ = m.resetState(tx, channelID)
		_ = tx.Commit()
		return applyResult{OK: false, RuinedAt: lastCount, Reason: "In trios you must wait for 2 other people to count."}, nil
	}

	var prevHigh int64
	_ = tx.QueryRow(
		`SELECT high_score FROM counting_channel_stats WHERE channel_id = ?;`,
		channelID,
	).Scan(&prevHigh)

	now := time.Now().Unix()

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
		return applyResult{}, err
	}

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
		return applyResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return applyResult{}, err
	}

	return applyResult{
		OK:        true,
		HighScore: newCount > prevHigh,
		Hit100:    newCount == 100,
	}, nil
}
