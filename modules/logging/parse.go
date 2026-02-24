package logging

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// parseUsernameFromFirstWord reads the first token as the username.
// We strip common trailing punctuation like ':' and ','
func parseUsernameFromFirstWord(content string) string {
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	name = strings.TrimSuffix(name, ":")
	name = strings.TrimSuffix(name, ",")
	name = strings.TrimSuffix(name, "-")
	name = strings.TrimSpace(name)
	return name
}

// parseTradeUsernames attempts to pull the sender (first embed) and receiver (second embed)
// usernames from embed title or bold-at-start of embed description.
func parseTradeUsernames(embeds []*discordgo.MessageEmbed) (sender, receiver string) {
	if len(embeds) >= 1 {
		sender = extractNameFromEmbed(embeds[0])
	}
	if len(embeds) >= 2 {
		receiver = extractNameFromEmbed(embeds[1])
	}
	return sender, receiver
}

func extractNameFromEmbed(e *discordgo.MessageEmbed) string {
	if e == nil {
		return ""
	}
	// Many bots place the username in the title.
	if strings.TrimSpace(e.Title) != "" {
		return stripMarkdownBold(strings.TrimSpace(e.Title))
	}
	// Screenshot suggests the username is bold at the top of the description.
	if strings.TrimSpace(e.Description) != "" {
		if name := extractLeadingBold(strings.TrimSpace(e.Description)); name != "" {
			return name
		}
		// Fallback: first line.
		line := strings.SplitN(strings.TrimSpace(e.Description), "\n", 2)[0]
		line = stripMarkdownBold(strings.TrimSpace(line))
		// If line is still long, assume it's not just a username.
		if len(strings.Fields(line)) == 1 {
			return line
		}
	}
	// Another common pattern is embed Author.Name.
	if e.Author != nil {
		if strings.TrimSpace(e.Author.Name) != "" {
			return stripMarkdownBold(strings.TrimSpace(e.Author.Name))
		}
	}
	return ""
}

func extractLeadingBold(s string) string {
	// Expect: **username** ...
	if !strings.HasPrefix(s, "**") {
		return ""
	}
	// Find closing **
	rest := s[2:]
	idx := strings.Index(rest, "**")
	if idx <= 0 {
		return ""
	}
	name := rest[:idx]
	name = strings.TrimSpace(name)
	return name
}

func stripMarkdownBold(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "**") && strings.HasSuffix(s, "**") && len(s) >= 4 {
		return strings.TrimSpace(s[2 : len(s)-2])
	}
	return s
}
