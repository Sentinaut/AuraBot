package levelling

import "database/sql"

type xpRow struct {
	UserID string
	XP     int64
}

func (m *Module) txGetUserXPAndLast(tx *sql.Tx, userID string) (xp int64, lastXPAt int64, err error) {
	err = tx.QueryRow(`SELECT xp, last_xp_at FROM user_xp WHERE user_id = ?`, userID).Scan(&xp, &lastXPAt)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return xp, lastXPAt, err
}

func (m *Module) txUpsertUserXP(tx *sql.Tx, userID, username string, xp int64, lastXPAt int64) error {
	_, err := tx.Exec(
		`INSERT INTO user_xp(user_id, username, xp, last_xp_at)
		 VALUES(?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   username = excluded.username,
		   xp = excluded.xp,
		   last_xp_at = excluded.last_xp_at`,
		userID, username, xp, lastXPAt,
	)
	return err
}

func (m *Module) getUserXP(guildID, userID string) (int64, error) {
	_ = guildID // intentionally ignored
	var xp int64
	err := m.db.QueryRow(`SELECT xp FROM user_xp WHERE user_id = ?`, userID).Scan(&xp)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return xp, err
}

func (m *Module) countXPUsers(guildID string) (int, error) {
	_ = guildID
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM user_xp`).Scan(&n)
	return n, err
}

func (m *Module) queryTopXPPage(guildID string, limit, offset int) ([]xpRow, error) {
	_ = guildID
	rows, err := m.db.Query(
		`SELECT user_id, xp
		 FROM user_xp
		 ORDER BY xp DESC
		 LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]xpRow, 0, limit)
	for rows.Next() {
		var r xpRow
		if err := rows.Scan(&r.UserID, &r.XP); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// List all XP users (optionally limited) for milestone backfill
func (m *Module) listAllXPUsers(limit int) ([]xpRow, error) {
	if limit > 0 {
		rows, err := m.db.Query(
			`SELECT user_id, xp
			 FROM user_xp
			 ORDER BY xp DESC
			 LIMIT ?`,
			limit,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		out := make([]xpRow, 0, limit)
		for rows.Next() {
			var r xpRow
			if err := rows.Scan(&r.UserID, &r.XP); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	rows, err := m.db.Query(
		`SELECT user_id, xp
		 FROM user_xp
		 ORDER BY xp DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]xpRow, 0, 256)
	for rows.Next() {
		var r xpRow
		if err := rows.Scan(&r.UserID, &r.XP); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// rank position = 1 + number of users strictly above this XP
func (m *Module) getRankPosition(guildID string, xp int64) (int64, error) {
	_ = guildID
	var n int64
	err := m.db.QueryRow(`SELECT COUNT(*) FROM user_xp WHERE xp > ?`, xp).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n + 1, nil
}

// Save (or overwrite) the saved message for a user's specific level.
func (m *Module) saveLevelUpMessage(userID, username string, level int, channelID, messageID, content string, createdAt int64) error {
	_, err := m.db.Exec(
		`INSERT OR REPLACE INTO level_up_messages
		 (user_id, username, level, channel_id, message_id, content, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		userID, username, level, channelID, messageID, content, createdAt,
	)
	return err
}

/*
XP curve / level math:

XP needed to go from level L -> L+1
*/
func xpNeededForNext(level int) int64 {
	l := int64(level)
	return 7*l*l + 60*l + 100
}

func levelForXP(totalXP int64) int {
	lvl, _, _ := breakdownXP(totalXP)
	return lvl
}

// breakdownXP returns:
// - level
// - xp into current level
// - xp needed to reach next level
func breakdownXP(totalXP int64) (int, int64, int64) {
	if totalXP <= 0 {
		return 0, 0, xpNeededForNext(0)
	}
	level := 0
	remaining := totalXP
	for {
		need := xpNeededForNext(level)
		if remaining < need {
			return level, remaining, need
		}
		remaining -= need
		level++
	}
}
