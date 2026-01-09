package levelling

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

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

// Reads milestone roles from environment variables.
//
// Supported formats:
//
//	LEVEL_ROLES="5:role,10:role,15:role"
//	LEVEL_ROLE_5="role"
//	LEVEL_ROLE_10="role"
//
// Individual LEVEL_ROLE_* entries override LEVEL_ROLES.
func parseLevelRolesFromEnv() map[int]string {
	out := map[int]string{}

	// Combined var (optional)
	if raw := strings.TrimSpace(os.Getenv("LEVEL_ROLES")); raw != "" {
		raw = strings.NewReplacer(";", ",", " ", ",").Replace(raw)
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			part = strings.ReplaceAll(part, "=", ":")
			kv := strings.SplitN(part, ":", 2)
			if len(kv) != 2 {
				continue
			}

			lvl, err := strconv.Atoi(strings.TrimSpace(kv[0]))
			if err != nil || lvl <= 0 {
				continue
			}
			roleID := strings.TrimSpace(kv[1])
			if roleID != "" {
				out[lvl] = roleID
			}
		}
	}

	// Auto-detect LEVEL_ROLE_<number>
	for _, env := range os.Environ() {
		kv := strings.SplitN(env, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := kv[0]
		val := strings.TrimSpace(kv[1])
		if val == "" || !strings.HasPrefix(key, "LEVEL_ROLE_") {
			continue
		}

		lvlStr := strings.TrimPrefix(key, "LEVEL_ROLE_")
		lvl, err := strconv.Atoi(lvlStr)
		if err != nil || lvl <= 0 {
			continue
		}

		out[lvl] = val
	}

	return out
}
