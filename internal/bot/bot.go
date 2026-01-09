package bot

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
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
