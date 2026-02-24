package logging

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onMessageCreate(s *discordgo.Session, ev *discordgo.MessageCreate) {
	if ev == nil || ev.Message == nil {
		return
	}

	msg := ev.Message
	if msg.Author != nil && s.State != nil && s.State.User != nil && msg.Author.ID == s.State.User.ID {
		return
	}
	// Prevent loops if the target is one of the source channels.
	if msg.ChannelID == m.targetChannelID {
		return
	}

	// Only care about configured channels.
	src := msg.ChannelID
	if src != m.tradeLogChannelID && src != m.storeLogChannelID && src != m.commandLogChannelID {
		return
	}

	shouldRepost := false

	// Trade logs
	if src == m.tradeLogChannelID {
		shouldRepost = m.shouldRepostTrade(msg)
	} else {
		// Store / command logs
		shouldRepost = m.shouldRepostSimple(msg.Content)
	}

	if !shouldRepost {
		return
	}

	content := strings.TrimSpace(msg.Content)

	_, err := s.ChannelMessageSendComplex(m.targetChannelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  msg.Embeds,
		// Avoid accidental pings when reposting logs.
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		log.Printf("[logging] repost failed (src=%s -> target=%s): %v", src, m.targetChannelID, err)
	}
}

func (m *Module) shouldRepostSimple(content string) bool {
	name := parseUsernameFromFirstWord(content)
	if name == "" {
		return false
	}
	_, ok := m.userSet[strings.ToLower(name)]
	return ok
}

func (m *Module) shouldRepostTrade(msg *discordgo.Message) bool {
	if !strings.HasPrefix(strings.TrimSpace(msg.Content), "New Trade!") {
		return false
	}

	sender, receiver := parseTradeUsernames(msg.Embeds)
	if sender == "" && receiver == "" {
		return false
	}

	if sender != "" {
		if _, ok := m.userSet[strings.ToLower(sender)]; ok {
			return true
		}
	}
	if receiver != "" {
		if _, ok := m.userSet[strings.ToLower(receiver)]; ok {
			return true
		}
	}
	return false
}

func discordMessageLink(guildID, channelID, messageID string) string {
	if guildID == "" || channelID == "" || messageID == "" {
		return ""
	}
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}
