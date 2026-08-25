package config

import "github.com/joho/godotenv"

var (
	AV_API_KEY string
)

func LoadApiKeys() error {
	dotenv, err := godotenv.Read(".env")
	if err != nil {
		return err
	}

	AV_API_KEY = dotenv["AV_API_KEY"]
	return nil
}
