package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Sentinaut/AuraBot/internal/bot"
	"github.com/Sentinaut/AuraBot/internal/db"
	"github.com/Sentinaut/AuraBot/modules/autoroles"
	"github.com/Sentinaut/AuraBot/modules/counting"
	"github.com/Sentinaut/AuraBot/modules/levelling"
	"github.com/Sentinaut/AuraBot/modules/starboard"
	"github.com/Sentinaut/AuraBot/modules/votingthreads"
	"github.com/Sentinaut/AuraBot/modules/welcoming"
)

const (
	ChannelSuggestions = "1474154596082385009" // #suggestions
	ChannelVotes       = "1474154463634784444" // #votes

	ChannelStarboard = "1474437470706991308" // #starboard

	// ⭐ Auto-react starboard channel
	ChannelAutoStar = "1474162005396160563" // #ingame-pics

	// 👋 Welcoming
	ChannelWelcome    = "1474171848282603542" // #welcome
	ChannelOnboarding = "1474437678509326397" // onboarding channel (no-role + staff)

	AutoRoleID = "1474137421175062561" // Members (granted AFTER username confirmed)

	// 🔢 Counting
	ChannelCounting      = "1474438358158544999" // #counting
	ChannelCountingTrios = "1474438390333309000" // #counting-trios

	CountingRuinedRoleID = "1474438491625492619" // role given on mess-up
)

// ⭐ Channels that count toward starboard (manual stars)
var StarChannels = []string{
	"1474003503809564676", // #hotel-chat
	"1474160250994163856", // #vip-chat
}

// ⭐ XP-enabled channels
var XPChannels = []string{
	"1474003503809564676", // #hotel-chat
	"1474153467294519307", // #off-topic
	"1474160250994163856", // #vip-chat
	"1474165200511959264", // #casino-chat
	"1474154178355138736", // #staff-chat
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
			AutoReact:  false,
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

		// 🔢 Counting (normal + trios) + ruined role for 16 hours
		counting.New(ChannelCounting, ChannelCountingTrios, CountingRuinedRoleID, 16*time.Hour, database.DB),

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

		// 👋 Welcoming (+ onboarding username thread)
		// Member role is granted only after username confirmed.
		welcoming.New(ChannelWelcome, ChannelOnboarding, AutoRoleID),
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := r.Run(); err != nil {
		log.Fatal(err)
	}
}
