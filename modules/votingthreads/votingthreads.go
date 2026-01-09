package votingthreads

import (
	"context"
	"database/sql"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type Module struct {
	allowedChannels map[string]struct{}
	db              *sql.DB
}

func New(channelIDs []string, db *sql.DB) *Module {
	m := &Module{
		allowedChannels: make(map[string]struct{}, len(channelIDs)),
		db:              db,
	}
	for _, id := range channelIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			m.allowedChannels[id] = struct{}{}
		}
	}
	return m
}

func (m *Module) Name() string { return "votingthreads" }

func (m *Module) Register(s *discordgo.Session) error {
	s.AddHandler(m.onMessageCreate)
	s.AddHandler(m.onMessageDelete)
	s.AddHandler(m.onMessageDeleteBulk)
	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error { return nil }

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil || e.Author == nil || e.Author.Bot {
		return
	}
	if _, ok := m.allowedChannels[e.ChannelID]; !ok {
		return
	}

	// React 👍 👎
	_ = s.MessageReactionAdd(e.ChannelID, e.ID, "👍")
	_ = s.MessageReactionAdd(e.ChannelID, e.ID, "👎")

	// Create thread attached to message
	threadName := makeThreadName(e.Content)
	thread, err := s.MessageThreadStart(e.ChannelID, e.ID, threadName, 1440)
	if err != nil {
		log.Printf("[votingthreads] thread create failed for msg %s: %v", e.ID, err)
		return
	}

	// Persist mapping
	_, err = m.db.Exec(
		`INSERT OR REPLACE INTO voting_threads(message_id, channel_id, thread_id, created_at)
		 VALUES(?,?,?, strftime('%s','now'))`,
		e.ID, e.ChannelID, thread.ID,
	)
	if err != nil {
		log.Printf("[votingthreads] db insert failed: %v", err)
	}
}

func (m *Module) onMessageDelete(s *discordgo.Session, e *discordgo.MessageDelete) {
	if e == nil {
		return
	}
	if _, ok := m.allowedChannels[e.ChannelID]; !ok {
		return
	}

	threadID, err := m.getThreadID(e.ID)
	if err != nil || threadID == "" {
		return
	}

	_, _ = s.ChannelDelete(threadID)

	_, _ = m.db.Exec(`DELETE FROM voting_threads WHERE message_id = ?`, e.ID)
}

func (m *Module) onMessageDeleteBulk(s *discordgo.Session, e *discordgo.MessageDeleteBulk) {
	if e == nil {
		return
	}
	if _, ok := m.allowedChannels[e.ChannelID]; !ok {
		return
	}

	for _, mid := range e.Messages {
		threadID, err := m.getThreadID(mid)
		if err != nil || threadID == "" {
			continue
		}
		_, _ = s.ChannelDelete(threadID)
		_, _ = m.db.Exec(`DELETE FROM voting_threads WHERE message_id = ?`, mid)
	}
}

func (m *Module) getThreadID(messageID string) (string, error) {
	var threadID string
	err := m.db.QueryRow(`SELECT thread_id FROM voting_threads WHERE message_id = ?`, messageID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return threadID, err
}

func makeThreadName(content string) string {
	clean := strings.TrimSpace(content)
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\n", " ")
	clean = strings.Join(strings.Fields(clean), " ")

	if clean == "" {
		return "Thread"
	}

	r := []rune(clean)
	if len(r) <= 50 {
		return clean
	}
	return string(r[:50]) + "..."
}
