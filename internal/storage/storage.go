package storage

import (
	"errors"
	"fmt"
)

type Storage struct {
	SSHUser *SSHUser
	User    *User
}

func (storage *BaseStorage) Storage() (*Storage, error) {
	sshUserStorage, err := storage.SSHUser()
	if err != nil {
		return nil, fmt.Errorf("unable to init SSH user storage: %w", err)
	}

	userStorage, err := storage.User()
	if err != nil {
		return nil, fmt.Errorf("unable to init user storage: %w", err)
	}

	res := &Storage{
		SSHUser: sshUserStorage,
		User:    userStorage,
	}
	return res, nil
}

func (storage *Storage) Reindex() error {
	sshUserErr := storage.SSHUser.Reindex()
	userErr := storage.User.Reindex()

	return errors.Join(sshUserErr, userErr)
}
