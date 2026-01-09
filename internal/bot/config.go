package bot

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Token string
}

func LoadConfig() (Config, error) {
	_ = godotenv.Load()

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		return Config{}, errors.New("DISCORD_TOKEN is required")
	}
	return Config{Token: token}, nil
}
