package starboard

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// hasImage returns true if a message has an image either as an attachment
// or via embed image/thumbnail.
func hasImage(msg *discordgo.Message) bool {
	if msg == nil {
		return false
	}

	for _, a := range msg.Attachments {
		if isImageAttachment(a) {
			return true
		}
	}
	for _, em := range msg.Embeds {
		if em == nil {
			continue
		}
		if em.Image != nil && em.Image.URL != "" {
			return true
		}
		if em.Thumbnail != nil && em.Thumbnail.URL != "" {
			return true
		}
	}
	return false
}

func isImageAttachment(a *discordgo.MessageAttachment) bool {
	if a == nil {
		return false
	}
	if a.ContentType != "" && strings.HasPrefix(a.ContentType, "image/") {
		return true
	}
	name := strings.ToLower(a.Filename)
	return strings.HasSuffix(name, ".png") ||
		strings.HasSuffix(name, ".jpg") ||
		strings.HasSuffix(name, ".jpeg") ||
		strings.HasSuffix(name, ".gif") ||
		strings.HasSuffix(name, ".webp")
}

func countStars(msg *discordgo.Message) int {
	if msg == nil {
		return 0
	}
	for _, r := range msg.Reactions {
		if r == nil || r.Emoji == nil {
			continue
		}
		if r.Emoji.Name == "⭐" {
			return r.Count
		}
	}
	return 0
}

func makeJumpURL(guildID, channelID, messageID string) string {
	return "https://discord.com/channels/" + guildID + "/" + channelID + "/" + messageID
}

// pickImageURL extracts the best image URL from a message (attachments first, then embeds).
func pickImageURL(msg *discordgo.Message) string {
	if msg == nil {
		return ""
	}
	for _, a := range msg.Attachments {
		if isImageAttachment(a) && a.URL != "" {
			return a.URL
		}
	}
	for _, em := range msg.Embeds {
		if em == nil {
			continue
		}
		if em.Image != nil && em.Image.URL != "" {
			return em.Image.URL
		}
		if em.Thumbnail != nil && em.Thumbnail.URL != "" {
			return em.Thumbnail.URL
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + (n % 10))
		n /= 10
	}
	return sign + string(b[i:])
}
