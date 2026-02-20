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
)

// NOTE:
// These are intentionally VARIABLES (not const) so they can be injected from cmd/bot/main.go.
// Other counting files can continue to reference emoji200/emoji500/emoji1000/customRuinerUserID/customRuinerGIFURL
// without needing edits.
var (
	emoji200  string
	emoji500  string
	emoji1000 string

	customRuinerUserID string
	customRuinerGIFURL string
)

type Module struct {
	db *sql.DB

	countingChannelID string
	triosChannelID    string

	ruinedRoleID string
	ruinedFor    time.Duration

	// Stored on the module too (useful if you later want m.emoji200 style access)
	emoji200  string
	emoji500  string
	emoji1000 string

	customRuinerUserID string
	customRuinerGIFURL string

	stop chan struct{}
}

// New creates the counting module.
// All IDs/URLs are passed in from cmd/bot/main.go so nothing is hardcoded inside the module.
func New(
	countingChannelID string,
	triosChannelID string,
	ruinedRoleID string,
	ruinedFor time.Duration,
	inEmoji200 string,
	inEmoji500 string,
	inEmoji1000 string,
	inCustomRuinerUserID string,
	inCustomRuinerGIFURL string,
	db *sql.DB,
) *Module {

	// Normalize values
	inEmoji200 = strings.TrimSpace(inEmoji200)
	inEmoji500 = strings.TrimSpace(inEmoji500)
	inEmoji1000 = strings.TrimSpace(inEmoji1000)
	inCustomRuinerUserID = strings.TrimSpace(inCustomRuinerUserID)
	inCustomRuinerGIFURL = strings.TrimSpace(inCustomRuinerGIFURL)

	// Set package-level vars so existing files can continue referencing them
	emoji200 = inEmoji200
	emoji500 = inEmoji500
	emoji1000 = inEmoji1000
	customRuinerUserID = inCustomRuinerUserID
	customRuinerGIFURL = inCustomRuinerGIFURL

	return &Module{
		db:                db,
		countingChannelID: strings.TrimSpace(countingChannelID),
		triosChannelID:    strings.TrimSpace(triosChannelID),
		ruinedRoleID:      strings.TrimSpace(ruinedRoleID),
		ruinedFor:         ruinedFor,

		emoji200:  inEmoji200,
		emoji500:  inEmoji500,
		emoji1000: inEmoji1000,

		customRuinerUserID: inCustomRuinerUserID,
		customRuinerGIFURL: inCustomRuinerGIFURL,

		stop: make(chan struct{}),
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
