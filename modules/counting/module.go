package counting

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"time"

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

	// Custom "ruined the count" user + media
	customRuinerUserID = "614628933337350149"
	customRuinerGIFURL = "https://media.tenor.com/QVe9jYKuGawAAAPo/sydney-trains-scrapping.mp4"
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
	// Slash commands are implemented in commands_*.go
	s.AddHandler(m.onReady)
	s.AddHandler(m.onInteractionCreate)

	// Counting message handler
	s.AddHandler(m.onMessageCreate)

	// Edited message handler (latest count edits + edits-to-number)
	s.AddHandler(m.onMessageUpdate)

	// Remove user-added tick reactions in counting channels
	s.AddHandler(m.onMessageReactionAdd)

	// Deleted message handlers
	s.AddHandler(m.onMessageDelete)
	s.AddHandler(m.onMessageDeleteBulk)

	return nil
}

func (m *Module) Start(ctx context.Context, s *discordgo.Session) error {
	// Schema is owned by internal/db/migrate.go, but we need ONE extra column for this feature.
	m.ensureDeleteTrackingSchema()

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

	log.Println("[counting] module started")
	return nil
}
