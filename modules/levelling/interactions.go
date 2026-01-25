package levelling

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		name := i.ApplicationCommandData().Name
		switch name {
		case "rank":
			m.handleRank(s, i)
		case "leaderboard":
			m.handleLeaderboard(s, i)
		case "joins":
			m.handleJoins(s, i)
		case "joinsbackfill":
			m.handleJoinsBackfill(s, i)
		case "levelupmsg":
			m.handleLevelUpMsg(s, i)
		case "levelupmsgset":
			m.handleLevelUpMsgSet(s, i)
		case "levelupmsgdelete":
			m.handleLevelUpMsgDelete(s, i)
		case "milestonesync":
			m.handleMilestoneSync(s, i)
		}

	case discordgo.InteractionMessageComponent:
		cid := i.MessageComponentData().CustomID
		if strings.HasPrefix(cid, "lb:") {
			m.handleLeaderboardComponent(s, i)
			return
		}
		if strings.HasPrefix(cid, "jn:") {
			m.handleJoinsComponent(s, i)
			return
		}
	}
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func (m *Module) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
