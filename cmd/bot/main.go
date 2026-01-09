package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/Sentinaut/AuraBot/internal/bot"
	"github.com/Sentinaut/AuraBot/internal/db"
	"github.com/Sentinaut/AuraBot/modules/autoroles"
	"github.com/Sentinaut/AuraBot/modules/levelling"
	"github.com/Sentinaut/AuraBot/modules/starboard"
	"github.com/Sentinaut/AuraBot/modules/votingthreads"
	"github.com/Sentinaut/AuraBot/modules/welcoming"
)

const (
	ChannelSuggestions = "1454964654596948233" // #suggestions
	ChannelVotes       = "1452677392127496224" // #votes

	ChannelStarboard = "1452582844210876458" // #starboard

	// ⭐ Auto-react starboard channel
	ChannelAutoStar = "1452584331917787216" // #ingame-pics

	// 👋 Welcoming
	ChannelWelcome = "1452582377858535644" // #welcome
	AutoRoleID     = "1424750509683904615" // Members
)

// ⭐ Channels that count toward starboard (manual stars)
var StarChannels = []string{
	"1452583868786806894", // #general-chat
	"1452584693672181770", // #builds
	"1452585428149338203", // #creators
}

// ⭐ XP-enabled channels
var XPChannels = []string{
	"1452583868786806894", // #general-chat
	"1452584013482164327", // #off-topic
	"1452613075659391049", // #staff-chat
	"1452622004732559410", // #support-chat
}

func main() {
	cfg, err := bot.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	// Place DB next to executable
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	baseDir := filepath.Dir(exe)

	dataDir := filepath.Join(baseDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatal(err)
	}

	dbPath := filepath.Join(dataDir, "aurabot.db")
	log.Println("DB PATH:", dbPath)

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if err := db.Migrate(database.DB); err != nil {
		log.Fatal(err)
	}

	// Build starboard channel rules
	starRules := map[string]starboard.ChannelRule{
		ChannelAutoStar: {AutoReact: true, Threshold: 6},
	}
	for _, ch := range StarChannels {
		starRules[ch] = starboard.ChannelRule{
			AutoReact: false,
			Threshold: 5,
		}
	}

	r, err := bot.NewRunner(cfg.Token, []bot.Module{
		// ⭐ Starboard system
		starboard.NewStarboard(
			starRules,
			ChannelStarboard,
			database.DB,
		),

		// ⭐ Starboard leaderboard command
		starboard.NewTopStars(database.DB),

		// ⭐ Levelling / XP system
		levelling.New(XPChannels, database.DB),

		// ✅ Autoroles (reaction roles)
		autoroles.New(database.DB),

		// 🗳️ Voting threads (👍👎 + auto thread)
		votingthreads.New(
			[]string{
				ChannelVotes,
				ChannelSuggestions,
			},
			database.DB,
		),

		// 👋 Welcoming
		welcoming.New(ChannelWelcome, AutoRoleID),
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := r.Run(); err != nil {
		log.Fatal(err)
	}
}
