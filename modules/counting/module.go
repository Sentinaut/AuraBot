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
	reactOK  = "✅"
	reactBad = "❌"
)

type Module struct {
	db *sql.DB

	countingChannelID string
	triosChannelID    string
}

func New(countingChannelID, triosChannelID string, db *sql.DB) *Module {
	return &Module{
		db:                db,
		countingChannelID: strings.TrimSpace(countingChannelID),
		triosChannelID:    strings.TrimSpace(triosChannelID),
	}
}

func (m *Module) Name() string { return "counting" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onMessageCreate)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error {
	// Schema is owned by internal/db/migrate.go
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

	okCount, err := m.applyCount(mode, e.ChannelID, e.Author.ID, n)
	if err != nil {
		log.Printf("[counting] apply error: %v", err)
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
		return
	}

	if okCount {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactOK)
	} else {
		_ = s.MessageReactionAdd(e.ChannelID, e.ID, reactBad)
	}
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

// applyCount enforces per-channel counting rules and persists state.
//
// Rules:
//  - Both modes: newCount must equal lastCount+1.
//  - Normal: same user cannot count twice in a row.
//  - Trios: user cannot count if they were one of the last TWO counters (3-person rotation).
func (m *Module) applyCount(mode channelMode, channelID, userID string, newCount int64) (bool, error) {
	if m.db == nil {
		return false, sql.ErrConnDone
	}

	tx, err := m.db.Begin()
	if err != nil {
		return false, err
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
			return false, err
		}
	}

	// Must be sequential
	if newCount != lastCount+1 {
		_ = tx.Commit() // commit no-op work
		return false, nil
	}

	// Spacing rule
	switch mode {
	case modeNormal:
		if lastUser != "" && userID == lastUser {
			_ = tx.Commit()
			return false, nil
		}
	case modeTrios:
		if (lastUser != "" && userID == lastUser) || (prevUser != "" && userID == prevUser) {
			_ = tx.Commit()
			return false, nil
		}
	}

	now := time.Now().Unix()

	// Upsert and shift history (prev <- last, last <- current)
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
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
