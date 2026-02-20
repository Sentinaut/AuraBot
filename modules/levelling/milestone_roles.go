package levelling

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// normalizeLevelRoles trims keys/values and drops empty entries
func normalizeLevelRoles(in map[int]string) map[int]string {
	out := map[int]string{}
	for lvl, roleID := range in {
		if lvl <= 0 {
			continue
		}
		roleID = strings.TrimSpace(roleID)
		if roleID == "" {
			continue
		}
		out[lvl] = roleID
	}
	return out
}

// Grants any configured milestone roles for levels in (oldLevel, newLevel].
// Add-only (stack roles). Never removes roles.
func (m *Module) applyMilestoneRoles(
	s *discordgo.Session,
	guildID string,
	userID string,
	oldLevel int,
	newLevel int,
) {
	if s == nil || guildID == "" || userID == "" {
		return
	}
	if len(m.levelRoles) == 0 || newLevel <= oldLevel {
		return
	}

	for lvl := oldLevel + 1; lvl <= newLevel; lvl++ {
		roleID := strings.TrimSpace(m.levelRoles[lvl])
		if roleID == "" {
			continue
		}

		if err := s.GuildMemberRoleAdd(guildID, userID, roleID); err != nil {
			log.Printf(
				"[levelling] milestone role add failed (user=%s level=%d role=%s): %v",
				userID, lvl, roleID, err,
			)
		}
	}
}
