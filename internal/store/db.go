package store

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/market"
	"dolores/internal/metrics"
	"dolores/internal/models"
)

const (
	defaultMongoURI = "mongodb://localhost:27017"
	defaultMongoDB  = "dolores"
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

func mongoURI() string {
	if uri := os.Getenv("MONGO_URI"); uri != "" {
		return uri
	}
	return defaultMongoURI
}

func mongoDBName() string {
	if name := os.Getenv("MONGO_DB"); name != "" {
		return name
	}
	return defaultMongoDB
}

func (s *Store) Init() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI()))
	if err != nil {
		return err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return err
	}
	s.Client = client
	s.DB = client.Database(mongoDBName())
	log.Printf("[store] connected to mongodb at %s, db=%s", mongoURI(), mongoDBName())
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

	if err := market.Migrate(s.DB); err != nil {
		return err
	}
	return metrics.Migrate(s.DB)
}

// IsNotFound reports whether err is a missing-document error.
func IsNotFound(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}
