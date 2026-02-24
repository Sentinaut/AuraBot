package welcoming

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const staffRoleID = "1475957292578115644"

func (m *Module) onMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Author == nil || e.Author.Bot {
		return
	}

	// Is this message in an onboarding thread we created?
	m.mu.Lock()
	var sess *onboardSession
	for _, v := range m.sessions {
		if v != nil && v.ThreadID == e.ChannelID {
			sess = v
			break
		}
	}
	m.mu.Unlock()

	if sess == nil {
		return
	}

	// Ignore empty content
	content := strings.TrimSpace(e.Content)
	if content == "" {
		return
	}

	// Save candidate username
	m.mu.Lock()
	sess.CandidateName = content
	m.mu.Unlock()

	sendConfirm(s, sess.ThreadID, content, sess.UserID)
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}

	// ───────────── Slash command: /toggleautoverify ─────────────
	if i.Type == discordgo.InteractionApplicationCommand {
		data := i.ApplicationCommandData()
		if data.Name != "toggleautoverify" {
			return
		}

		m.mu.Lock()
		m.autoVerifyEnabled = !m.autoVerifyEnabled
		enabled := m.autoVerifyEnabled
		m.mu.Unlock()

		state := "OFF"
		if enabled {
			state = "ON"
		}

		_ = s.InteractionRespond(i.Interaction, ephemeral("Auto-verify is now **"+state+"**.\n\nON = set nickname + add member role + remove unverified\nOFF = set nickname only"))
		return
	}

	// ───────────── Button clicks ─────────────
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	customID := i.MessageComponentData().CustomID
	if !strings.HasPrefix(customID, "welcoming:") {
		return
	}

	parts := strings.Split(customID, ":")
	if len(parts) != 3 {
		_ = s.InteractionRespond(i.Interaction, ephemeral("Invalid button payload."))
		return
	}
	action := parts[1]
	targetUserID := parts[2]

	clickerID := ""
	if i.Member != nil && i.Member.User != nil {
		clickerID = i.Member.User.ID
	}
	if clickerID == "" {
		_ = s.InteractionRespond(i.Interaction, ephemeral("Could not identify user."))
		return
	}

	if clickerID != targetUserID {
		_ = s.InteractionRespond(i.Interaction, ephemeral("These buttons aren’t for you."))
		return
	}

	m.mu.Lock()
	sess := m.sessions[targetUserID]
	autoVerify := m.autoVerifyEnabled
	m.mu.Unlock()

	if sess == nil {
		_ = s.InteractionRespond(i.Interaction, ephemeral("Your onboarding session has expired or was not found."))
		return
	}

	// Disable buttons
	if i.Message != nil {
		components := disableAllButtons(i.Message)
		if components != nil {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Components: &components,
			})
		}
	}

	if action == "no" {
		_ = s.InteractionRespond(i.Interaction, ephemeral("No worries — reply again in the thread with the username you want."))
		return
	}

	if action != "yes" {
		_ = s.InteractionRespond(i.Interaction, ephemeral("Unknown action."))
		return
	}

	m.mu.Lock()
	name := strings.TrimSpace(sess.CandidateName)
	m.mu.Unlock()

	if name == "" {
		_ = s.InteractionRespond(i.Interaction, ephemeral("Please reply in the thread with the username you want first."))
		return
	}

	// Set nickname
	if err := s.GuildMemberNickname(sess.GuildID, targetUserID, name); err != nil {
		log.Printf("[welcoming] failed to set nickname: %v", err)
		_ = s.InteractionRespond(i.Interaction, ephemeral("I couldn’t set your nickname (missing permissions?)."))
		return
	}

	// Auto verify ON
	if autoVerify {
		if m.memberRoleID != "" {
			_ = s.GuildMemberRoleAdd(sess.GuildID, targetUserID, m.memberRoleID)
		}
		if m.unverifiedRoleID != "" {
			_ = s.GuildMemberRoleRemove(sess.GuildID, targetUserID, m.unverifiedRoleID)
		}
	} else {
		// ✅ Auto verify OFF → notify staff
		if m.onboardingChannelID != "" {
			msg := "<@&" + staffRoleID + "> <@" + targetUserID + "> has set their username and needs verification."
			if _, err := s.ChannelMessageSend(m.onboardingChannelID, msg); err != nil {
				log.Printf("[welcoming] failed to notify staff: %v", err)
			}
		}
	}

	_ = s.InteractionRespond(i.Interaction, ephemeral("✅ Done! Your nickname has been set to **"+escapeMarkdown(name)+"**."))

	// Cleanup
	m.mu.Lock()
	delete(m.sessions, targetUserID)
	m.mu.Unlock()

	if sess.ThreadID != "" {
		_, _ = s.ChannelDelete(sess.ThreadID)
	}
	if sess.ParentMsgID != "" && m.onboardingChannelID != "" {
		_ = s.ChannelMessageDelete(m.onboardingChannelID, sess.ParentMsgID)
	}
}
