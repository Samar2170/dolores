package config

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

var (
	ALPHAVANTAGE_API_KEY string
	INDIAN_SM_API_KEY    string
	INDIAN_SM_API_KEY2   string
	ARCHIVUS_API_KEY     string
	ARCHIVUS_BASE_URL    string
	OPENROUTER_API_KEY   string
)

const defaultStorageParentFolder = "financial_data"

type RunningConfig struct {
	ALLOWED_MODELS        []string `yaml:"ALLOWED_MODELS"`
	NOTIFICATION_CHANNELS []string `yaml:"NOTIFICATION_CHANNELS"`
	STORAGE_PARENT_FOLDER []string `yaml:"STORAGE_PARENT_FOLDER"`
}

var Config RunningConfig

func LoadDefaultConfigs() error {
	if err := LoadApiKeys(); err != nil {
		return err
	}

	data, err := os.ReadFile("config.yaml")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := yaml.Unmarshal(data, &Config); err != nil {
		return fmt.Errorf("parse config.yaml: %w", err)
	}
	log.Println("Loaded config.yaml:",
		len(Config.ALLOWED_MODELS), "models,", len(Config.STORAGE_PARENT_FOLDER), "storage parent folders")
	return nil
}

// StorageParentFolder returns the configured root folder for archived data,
// falling back to the project default when unset.
func StorageParentFolder() string {
	for _, f := range Config.STORAGE_PARENT_FOLDER {
		if f != "" {
			return f
		}
	}
	return defaultStorageParentFolder
}

func LoadApiKeys() error {
	dotenv, err := godotenv.Read(".env")
	if err != nil {
		return err
	}
	ALPHAVANTAGE_API_KEY = loadConfigVar(&dotenv, "ALPHAVANTAGE_API_KEY")
	ARCHIVUS_API_KEY = loadConfigVar(&dotenv, "ARCHIVUS_API_KEY")
	ARCHIVUS_BASE_URL = loadConfigVar(&dotenv, "ARCHIVUS_BASE_URL")
	OPENROUTER_API_KEY = loadConfigVar(&dotenv, "OPENROUTER_API_KEY")
	INDIAN_SM_API_KEY = loadConfigVar(&dotenv, "INDIAN_SM_API_KEY")
	INDIAN_SM_API_KEY2 = loadConfigVar(&dotenv, "INDIAN_SM_API_KEY2")
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
