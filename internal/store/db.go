package store

import (
	"context"
	"dolores/config"
	"dolores/internal/models"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Store struct {
	Client         *mongo.Client
	DB             *mongo.Database
	ProjectBaseDir string
}

var ErrRecordNotFound = mongo.ErrNoDocuments

func GetStore(projectBaseDir string) (*Store, error) {
	s := &Store{ProjectBaseDir: projectBaseDir}
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s, nil
}

func mongoURI() (string, error) {
	uri := config.MONGO_URI
	if uri != "" {
		return uri, nil
	}
	return "", fmt.Errorf("MONGO_URI environment variable is not set")
}

func mongoDBName() (string, error) {
	if name := config.MONGO_DB_NAME; name != "" {
		return name, nil
	}
	return "", fmt.Errorf("MONGO_DB environment variable is not set")
}

func (s *Store) Init() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoUri, err := mongoURI()
	if err != nil {
		return err
	}
	mongoDBName, err := mongoDBName()
	if err != nil {
		return err
	}
	client, err := mongo.Connect(options.Client().ApplyURI(mongoUri))
	if err != nil {
		return err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return err
	}
	s.Client = client

	s.DB = client.Database(mongoDBName)
	log.Printf("[store] connected to mongodb at %s, db=%s", mongoUri, mongoDBName)
	return nil
}

func (s *Store) Close() error {
	if s.Client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.Client.Disconnect(ctx)
}

func (s *Store) Collection(name string) *mongo.Collection {
	return s.DB.Collection(name)
}

// Migrate ensures the required collections and indexes exist.
// Extra bson documents passed are ignored (kept for API compatibility).
func (s *Store) Migrate(_ ...interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := s.DB.Collection(models.ColCompanies).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "exchange", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_company_symbol_exchange"),
		},
	})
	if err != nil {
		return err
	}

	_, err = s.DB.Collection(models.ColCompanyLinks).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "company_id", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_company_links_company"),
	})
	if err != nil {
		return err
	}

	_, err = s.DB.Collection(models.ColCompanyResources).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "company_id", Value: 1}, {Key: "analysis_segment", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_crr_company_segment"),
	})
	if err != nil {
		return err
	}

	_, err = s.DB.Collection(models.ColAnalysisFiles).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "company_id", Value: 1}, {Key: "file_name", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_analysis_files_company_file"),
	})
	if err != nil {
		return err
	}
	return nil
	// if err := market.Migrate(s.DB); err != nil {
	// 	return err
	// }
	// if err := metrics.Migrate(s.DB); err != nil {
	// 	return err
	// }
	// return analysis.Migrate(s.DB)
}

// IsNotFound reports whether err is a missing-document error.
func IsNotFound(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}
