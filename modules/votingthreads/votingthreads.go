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

	// If this message is a reply to another message, block it and DM the author.
	// This enforces "no replies in the channel — use the generated thread".
	//
	// NOTE: In newer discordgo versions, Reference is a METHOD: e.Message.Reference()
	ref := e.Message.Reference()
	if ref != nil && ref.MessageID != "" {
		refMsgID := ref.MessageID

		threadID, err := m.getThreadID(refMsgID)
		if err != nil {
			log.Printf("[votingthreads] failed to lookup thread for reply: %v", err)
			// fallthrough: if lookup fails, we won't delete to avoid false positives
		} else if threadID != "" {
			// Delete the reply in-channel
			_ = s.ChannelMessageDelete(e.ChannelID, e.ID)

			// Try to DM the user telling them to use the thread instead.
			dm, err := s.UserChannelCreate(e.Author.ID)
			if err != nil {
				log.Printf("[votingthreads] failed to create DM channel for %s: %v", e.Author.ID, err)
				return
			}

			// Mention the thread channel so the user can click it (<#threadID>).
			msg := "Replies are not allowed in this channel. Please discuss in the thread for that message: <#" + threadID + ">"
			_, _ = s.ChannelMessageSend(dm.ID, msg) // ignore errors; user may have DMs closed

			return
		}
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
	e := m.db.QueryRow(`SELECT thread_id FROM voting_threads WHERE message_id = ?`, messageID).Scan(&threadID)
	if e == sql.ErrNoRows {
		return "", nil
	}
	return threadID, e
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
