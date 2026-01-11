package storage

import (
	"fmt"
	"sync"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/util"
)

type User struct {
	storage    *Storage
	idIndex    map[int64]struct{}
	indexMutex sync.Mutex
}

const (
	userDirName = "user"
)

func (storage *Storage) User() (*User, error) {
	user := &User{
		storage: storage,
		idIndex: make(map[int64]struct{}),
	}

	if err := storage.initIndexes(userDirName, user.initIndexes); err != nil {
		return nil, fmt.Errorf("unable to initialize user indexes: %w", err)
	}

	return user, nil
}

func (storage *User) ExistsByID(id int64) (bool, error) {
	return util.Synchronized(&storage.indexMutex, func() (bool, error) {
		_, ok := storage.idIndex[id]
		return ok, nil
	})
}

func (storage *User) FindByID(id int64) (*model.User, error) {
	if ok, err := storage.ExistsByID(id); !ok || err != nil {
		return nil, err
	}

	return storage.readUser(id)
}

func (storage *User) Save(user *model.User) (*model.User, error) {
	if user.ID == 0 {
		return nil, fmt.Errorf("user ID must be a valid TG ID")
	}

	return util.Synchronized(&storage.indexMutex, func() (*model.User, error) {
		if oldUser, _ := storage.readUser(user.ID); oldUser != nil {
			storage.dropIndexesUser(oldUser)
		}

		if err := storage.saveUser(user); err != nil {
			return nil, err
		}

		storage.initIndexesUser(user)
		return user, nil
	})
}

func (storage *User) initIndexes(id model.ID) error {
	tgID, err := model.IDToInt64(id)
	if err != nil {
		return err
	}

	user, err := storage.readUser(tgID)
	if err != nil {
		return err
	}

	storage.initIndexesUser(user)
	return nil
}

func (storage *User) initIndexesUser(user *model.User) {
	storage.idIndex[user.ID] = struct{}{}
}

func (storage *User) dropIndexesUser(user *model.User) {
	delete(storage.idIndex, user.ID)
}

func (storage *User) readUser(id int64) (*model.User, error) {
	var user model.User

	if err := storage.storage.readFile(userDirName, model.Int64ToID(id), &user); err != nil {
		return nil, fmt.Errorf("unable to read user %v: %w", id, err)
	}

	user.ID = id
	return &user, nil
}

func (storage *User) saveUser(user *model.User) error {
	if err := storage.storage.saveFile(userDirName, model.Int64ToID(user.ID), maybeCreate, user); err != nil {
		return fmt.Errorf("unable to save user %v: %w", user.ID, err)
	}

	return nil
}
