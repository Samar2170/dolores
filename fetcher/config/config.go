package config

import (
	"log"

	"github.com/joho/godotenv"
)

var (
	AV_API_KEY        string
	ARCHIVUS_API_KEY  string
	ARCHIVUS_BASE_URL string
	INDIA_SM_API_KEY  string
)

func LoadApiKeys() error {
	dotenv, err := godotenv.Read(".env")
	if err != nil {
		return err
	}
	AV_API_KEY = loadConfigVar(&dotenv, "AV_API_KEY")
	ARCHIVUS_API_KEY = loadConfigVar(&dotenv, "ARCHIVUS_API_KEY")
	ARCHIVUS_BASE_URL = loadConfigVar(&dotenv, "ARCHIVUS_BASE_URL")
	INDIA_SM_API_KEY = loadConfigVar(&dotenv, "INDIA_SM_API_KEY")
	return nil
}

func loadConfigVar(envVars *map[string]string, key string) string {
	val := (*envVars)[key]
	if val == "" {
		log.Println("Warning: environment variable", key, "is not set")
	} else {
		log.Println("Loaded environment variable ", key, "=", hideValue(val), "from environment")
	}
	return val
}

func hideValue(val string) string {
	if len(val) <= 4 {
		return "****"
	}
	return val[:2] + "****" + val[len(val)-2:]
}
