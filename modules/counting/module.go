package counting

import (
	"context"
	"database/sql"
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
}

func New(countingChannelID, triosChannelID, ruinedRoleID string, ruinedFor time.Duration, db *sql.DB) *Module {
	return &Module{
		db:                db,
		countingChannelID: strings.TrimSpace(countingChannelID),
		triosChannelID:    strings.TrimSpace(triosChannelID),
		ruinedRoleID:      strings.TrimSpace(ruinedRoleID),
		ruinedFor:         ruinedFor,
	}
}

func (m *Module) Name() string { return "counting" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onMessageCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error {
	return nil
}

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil || e.Author == nil || e.Author.Bot {
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

	res, err := m.applyCount(mode, e.ChannelID, e.Author.ID, n)
	if err != nil {
		log.Printf("[counting] error: %v", err)
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
		return
	}

	if !res.OK {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
		return
	}

	// Base reaction
	if res.HighScore {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHighScore)
	} else {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactOK)
	}

	// Milestones
	if res.Hit100 {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactHundred)
	}

	switch res.Count {
	case 200:
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji200)
	case 500:
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji500)
	case 1000:
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, emoji1000)
	}
}

type channelMode int

const (
	modeDisabled channelMode = iota
	modeNormal
	modeTrios
)

func (m *Module) channelMode(channelID string) channelMode {
	if channelID == m.countingChannelID {
		return modeNormal
	}
	if channelID == m.triosChannelID {
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
	HighScore bool
	Hit100    bool
	Count     int64
}

func (m *Module) applyCount(mode channelMode, channelID, userID string, newCount int64) (applyResult, error) {
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
		 WHERE channel_id = ?`,
		channelID,
	).Scan(&lastCount, &lastUser, &prevUser)

	if err == sql.ErrNoRows {
		lastCount = 0
	} else if err != nil {
		return applyResult{}, err
	}

	if newCount != lastCount+1 {
		_, _ = tx.Exec(`DELETE FROM counting_state WHERE channel_id = ?`, channelID)
		_ = tx.Commit()
		return applyResult{OK: false}, nil
	}

	if mode == modeNormal && userID == lastUser {
		_, _ = tx.Exec(`DELETE FROM counting_state WHERE channel_id = ?`, channelID)
		_ = tx.Commit()
		return applyResult{OK: false}, nil
	}

	if mode == modeTrios && (userID == lastUser || userID == prevUser) {
		_, _ = tx.Exec(`DELETE FROM counting_state WHERE channel_id = ?`, channelID)
		_ = tx.Commit()
		return applyResult{OK: false}, nil
	}

	var prevHigh int64
	_ = tx.QueryRow(
		`SELECT high_score FROM counting_channel_stats WHERE channel_id = ?`,
		channelID,
	).Scan(&prevHigh)

	_, err = tx.Exec(
		`INSERT INTO counting_state (channel_id, last_count, last_user_id, prev_user_id)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
			last_count = excluded.last_count,
			prev_user_id = counting_state.last_user_id,
			last_user_id = excluded.last_user_id`,
		channelID, newCount, userID, prevUser,
	)
	if err != nil {
		return applyResult{}, err
	}

	_, err = tx.Exec(
		`INSERT INTO counting_channel_stats (channel_id, high_score)
		 VALUES (?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
			high_score = MAX(high_score, excluded.high_score)`,
		channelID, newCount,
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
		Count:     newCount,
	}, nil
}
