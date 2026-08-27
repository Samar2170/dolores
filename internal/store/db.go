package store

import (
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	DB             *gorm.DB
	tx             *gorm.DB
	ProjectBaseDir string
}

var (
	ErrRecordNotFound = gorm.ErrRecordNotFound
)

func GetStore(projectBaseDir string) (*Store, error) {
	s := &Store{ProjectBaseDir: projectBaseDir}
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Init() error {
	dbFile := s.getStorageDbFile("storage.db")
	db, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return err
	}
	s.DB = db
	return nil
}

func (s *Store) conn() *gorm.DB {
	if s.tx != nil {
		return s.tx
	}
	return s.DB
}

func (s *Store) WithTx(tx *gorm.DB) *Store {
	return &Store{DB: s.DB, tx: tx}
}

func (s *Store) Transaction(fn func(tx *Store) error) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return fn(s.WithTx(tx))
	})
}

func (s *Store) getStorageDbFile(dbFile string) string {
	return filepath.Join(s.ProjectBaseDir, dbFile)
}

func (s *Store) Migrate(models ...interface{}) error {
	return s.DB.AutoMigrate(models...)
}
