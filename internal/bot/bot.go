package bot

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Module interface {
	Name() string
	Register(s *discordgo.Session) error
	Start(ctx context.Context, s *discordgo.Session) error
}

type Runner struct {
	Session *discordgo.Session
	Modules []Module

	cleanupOnce sync.Once
}

func NewRunner(token string, modules []Module) (*Runner, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	// ✅ REQUIRED for welcoming (GuildMemberAdd)
	s.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMembers | // 👈 THIS LINE
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent

	return &Runner{Session: s, Modules: modules}, nil
}

func (r *Runner) Run() error {
	// If you're running AuraBot in a single guild, old GLOBAL slash commands can
	// hang around and show as duplicates alongside GUILD commands.
	//
	// We wipe all GLOBAL commands once on Ready when GUILD_ID is set.
	r.Session.AddHandler(r.onReadyGlobalCommandCleanup)

	for _, m := range r.Modules {
		if err := m.Register(r.Session); err != nil {
			return err
		}
		log.Printf("registered module: %s", m.Name())
	}

	if err := r.Session.Open(); err != nil {
		return err
	}
	defer r.Session.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, m := range r.Modules {
		if err := m.Start(ctx, r.Session); err != nil {
			return err
		}
		log.Printf("started module: %s", m.Name())
	}

	log.Println("AuraBot is running. Press Ctrl+C to stop.")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	cancel()
	time.Sleep(300 * time.Millisecond)
	return nil
}

func (r *Runner) onReadyGlobalCommandCleanup(s *discordgo.Session, _ *discordgo.Ready) {
	r.cleanupOnce.Do(func() {
		guildID := strings.TrimSpace(os.Getenv("GUILD_ID"))
		if guildID == "" {
			// Not in single-guild mode (or GUILD_ID not set). Do nothing.
			return
		}

		appID := ""
		if s.State != nil && s.State.User != nil {
			appID = s.State.User.ID
		}
		if appID == "" {
			log.Println("[bot] global command cleanup skipped: missing application ID")
			return
		}

		// Bulk overwrite GLOBAL commands with an empty list = delete all globals.
		// This prevents the Discord client from showing global+guild duplicates.
		if _, err := s.ApplicationCommandBulkOverwrite(appID, "", []*discordgo.ApplicationCommand{}); err != nil {
			log.Printf("[bot] global command cleanup failed: %v", err)
			return
		}

		log.Printf("[bot] cleared all GLOBAL slash commands (single-guild mode: %s)", guildID)
	})
}
