package texttalk

import (
	"bufio"
	"context"
	"log"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type Module struct {
	session     *discordgo.Session
	channelID   string
	channelName string
	originalID  string // Store the original channel ID
}

// New function to initialize the module
func New() *Module {
	return &Module{}
}

// Register method to initialize the module with a session
func (m *Module) Register(session *discordgo.Session) error {
	m.session = session // Access the bot's session

	// Fetch the channel ID from environment variable or set a default one
	m.channelID = strings.TrimSpace(os.Getenv("TEXTTALK_CHANNEL_ID"))
	if m.channelID == "" {
		log.Println("[texttalk] TEXTTALK_CHANNEL_ID not set, module disabled")
		return nil
	}

	// Store the original channel ID
	m.originalID = m.channelID

	// Fetch the channel information (including name) using the channel ID
	channel, err := m.session.Channel(m.channelID)
	if err != nil {
		log.Printf("[texttalk] Error fetching channel: %v", err)
		return err
	}

	// Store the channel name
	m.channelName = channel.Name

	// Log the channel ID and name where the bot is talking
	log.Printf("[texttalk] Currently talking in the %s channel (%s)", m.channelName, m.channelID)

	// Start reading console input in a goroutine
	go m.readConsole()

	return nil
}

// Init method (for custom initialization if needed)
func (m *Module) Init() error {
	return nil
}

// Name method to return the name of the module
func (m *Module) Name() string {
	return "texttalk"
}

// Start method must match the signature expected by the bot.Module interface
// Now accepting context and session parameters
func (m *Module) Start(ctx context.Context, session *discordgo.Session) error {
	// This method is required but not needed in this case
	return nil
}

// Stop method is required but not needed in this case
func (m *Module) Stop() error {
	return nil
}

// readConsole listens for user input in the console and sends messages to the specified channel
func (m *Module) readConsole() {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		if !scanner.Scan() {
			return
		}

		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		// Handle the /quit and /exit commands
		if text == "/quit" || text == "/exit" {
			log.Println("[texttalk] exit command received")
			os.Exit(0)
		}

		// Handle the /changechannel command
		if strings.HasPrefix(text, "/changechannel") {
			m.changeChannel(text)
			continue
		}

		// Handle the /defaultchannel command
		if text == "/defaultchannel" {
			m.changeToDefaultChannel()
			continue
		}

		// Send the message to the current channel
		_, err := m.session.ChannelMessageSend(m.channelID, text)
		if err != nil {
			log.Println("[texttalk] send failed:", err)
		}
	}
}

// changeChannel handles changing the channel ID dynamically from the console
func (m *Module) changeChannel(command string) {
	// Extract the new channel ID from the command
	parts := strings.Split(command, " ")
	if len(parts) != 2 {
		log.Println("[texttalk] Invalid command format. Usage: /changechannel {channelid}")
		return
	}
	newChannelID := parts[1]

	// Fetch the new channel info
	channel, err := m.session.Channel(newChannelID)
	if err != nil {
		log.Printf("[texttalk] Error fetching new channel: %v", err)
		return
	}

	// Update the channel ID and name
	m.channelID = newChannelID
	m.channelName = channel.Name

	// Log the change
	log.Printf("[texttalk] Changed to %s channel (%s)", m.channelName, m.channelID)
}

// changeToDefaultChannel resets the channel to the original channel
func (m *Module) changeToDefaultChannel() {
	// Fetch the original channel info
	channel, err := m.session.Channel(m.originalID)
	if err != nil {
		log.Printf("[texttalk] Error fetching default channel: %v", err)
		return
	}

	// Reset the channel ID and name to the original
	m.channelID = m.originalID
	m.channelName = channel.Name

	// Log the reset
	log.Printf("[texttalk] Changed to original channel %s (%s)", m.channelName, m.channelID)
}
