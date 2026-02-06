package counting

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode"
)

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
func (m *Module) applyCount(mode channelMode, guildID, channelID, userID, username, messageID string, newCount int64) (applyResult, error) {
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
		`INSERT INTO counting_state (channel_id, last_count, last_user_id, prev_user_id, updated_at, last_message_id)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
			last_count = excluded.last_count,
			prev_user_id = counting_state.last_user_id,
			last_user_id = excluded.last_user_id,
			updated_at = excluded.updated_at,
			last_message_id = excluded.last_message_id;`,
		channelID, newCount, userID, prevUser, now, messageID,
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
		`INSERT INTO counting_state (channel_id, last_count, last_user_id, prev_user_id, updated_at, last_message_id)
		 VALUES (?, 0, '', '', ?, '')
		 ON CONFLICT(channel_id) DO UPDATE SET
			last_count = 0,
			last_user_id = '',
			prev_user_id = '',
			updated_at = excluded.updated_at,
			last_message_id = '';`,
		channelID, now,
	)
	return err
}
