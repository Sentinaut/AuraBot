package levelling

import (
	"errors"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// getGuildMemberIDSet returns a set of user IDs currently in the guild.
// It caches results briefly to avoid hammering the API when paging leaderboards.
func (m *Module) getGuildMemberIDSet(s *discordgo.Session, guildID string) (map[string]struct{}, error) {
	guildID = strings.TrimSpace(guildID)
	if s == nil || guildID == "" {
		return nil, errors.New("missing session or guildID")
	}

	m.members.mu.Lock()
	// cache TTL
	if m.members.guildID == guildID && m.members.ids != nil && time.Since(m.members.fetched) < 2*time.Minute {
		ids := m.members.ids
		m.members.mu.Unlock()
		return ids, nil
	}
	m.members.mu.Unlock()

	ids := map[string]struct{}{}
	after := ""
	for {
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			break
		}
		for _, mem := range members {
			if mem == nil || mem.User == nil || mem.User.ID == "" {
				continue
			}
			ids[mem.User.ID] = struct{}{}
			after = mem.User.ID
		}
		if len(members) < 1000 {
			break
		}
	}

	m.members.mu.Lock()
	m.members.guildID = guildID
	m.members.ids = ids
	m.members.fetched = time.Now()
	m.members.mu.Unlock()

	return ids, nil
}
