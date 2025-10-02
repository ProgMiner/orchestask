package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

import (
	"bypm.ru/orchestask/model"
)

type Storage struct {
	basePath     string
	lockFilePath string
}

func Init(basePath string) (*Storage, error) {
	s := &Storage{basePath: filepath.Clean(basePath)}

	if _, err := s.ensureDir(""); err != nil {
		return nil, fmt.Errorf("wrong storage base path: %w", err)
	}

	if err := s.makeLockFile(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Storage) makeLockFile() error {
	s.lockFilePath = filepath.Join(s.basePath, ".lock")

	lockFile, err := os.OpenFile(s.lockFilePath, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0000)
	if err != nil {
		return fmt.Errorf("unable to create lockfile: %w", err)
	}

	if err := lockFile.Close(); err != nil {
		return fmt.Errorf("unexpected error: %w", err)
	}

	return nil
}

func (s *Storage) Close() error {
	if err := os.Remove(s.lockFilePath); err != nil {
		return fmt.Errorf("unable to delete lockfile: %w", err)
	}

	return nil
}

func (storage *Storage) ensureDir(dir string) (string, error) {
	path := filepath.Join(storage.basePath, dir)
	return path, os.MkdirAll(path, 0755)
}

func (storage *Storage) initIndexes(dir string, handler func(id model.ID) error) error {
	path, err := storage.ensureDir(dir)
	if err != nil {
		return err
	}

	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, file := range files {
		id := model.ID(file.Name())

		if err := handler(id); err != nil {
			return fmt.Errorf("unable to init %v: %w", id, err)
		}
	}

	return nil
}

func (storage *Storage) readFile(dir string, id model.ID, result any) error {
	path := filepath.Join(storage.basePath, dir, string(id))

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to open file %s: %w", path, err)
	}

	defer func() {
		if err1 := file.Close(); err1 != nil {
			err = fmt.Errorf("unable to close file %s: %w", path, err)
		}
	}()

	if err := json.NewDecoder(file).Decode(result); err != nil {
		return fmt.Errorf("unable to decode JSON (%s): %w", path, err)
	}

	return err
}

func (storage *Storage) saveFile(dir string, id model.ID, data any) error {
	path := filepath.Join(storage.basePath, dir, string(id))

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("unable to open file %s: %w", path, err)
	}

	defer func() {
		if err1 := file.Close(); err1 != nil {
			err = fmt.Errorf("unable to close file %s: %w", path, err)
		}
	}()

	if err := json.NewEncoder(file).Encode(data); err != nil {
		return fmt.Errorf("unable to encode JSON (%s): %w", path, err)
	}

	return err
}
