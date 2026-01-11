package storage

import (
	"fmt"
	"sync"
)

import (
	"github.com/google/uuid"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/util"
)

type SSHUser struct {
	storage      *BaseStorage
	idIndex      map[model.ID]struct{}
	sshPKeyIndex map[string]model.ID
	indexMutex   sync.Mutex
}

const (
	sshUserDirName = "ssh-user"
)

func (storage *BaseStorage) SSHUser() (*SSHUser, error) {
	user := &SSHUser{storage: storage}

	if err := user.reindex(); err != nil {
		return nil, err
	}

	return user, nil
}

func (storage *SSHUser) FindByPKey(pkey string) (*model.SSHUser, error) {
	id, ok := util.Synchronized(&storage.indexMutex, func() (model.ID, bool) {
		id, ok := storage.sshPKeyIndex[pkey]
		return id, ok
	})

	if !ok {
		return nil, nil
	}

	return storage.readUser(id)
}

func (storage *SSHUser) ExistsByID(id model.ID) (bool, error) {
	return util.Synchronized(&storage.indexMutex, func() (bool, error) {
		_, ok := storage.idIndex[id]
		return ok, nil
	})
}

func (storage *SSHUser) FindByID(id model.ID) (*model.SSHUser, error) {
	if ok, err := storage.ExistsByID(id); !ok || err != nil {
		return nil, err
	}

	return storage.readUser(id)
}

func (storage *SSHUser) generateID() model.ID {
	for {
		id := model.StringToID(uuid.NewString())
		if _, ok := storage.idIndex[id]; !ok {
			return id
		}
	}
}

func (storage *SSHUser) Save(user *model.SSHUser) (*model.SSHUser, error) {
	isNew := false

	if user.ID == "" {
		user.ID = storage.generateID()
		isNew = true
	}

	return util.Synchronized(&storage.indexMutex, func() (*model.SSHUser, error) {
		if !isNew {
			if oldUser, _ := storage.readUser(user.ID); oldUser != nil {
				storage.dropIndexesUser(oldUser)
			}
		}

		if err := storage.saveUser(user, isNew); err != nil {
			return nil, err
		}

		storage.initIndexesUser(user)
		return user, nil
	})
}

func (storage *SSHUser) Reindex() error {
	_, err := util.Synchronized(&storage.indexMutex, func() (*struct{}, error) {
		return nil, storage.reindex()
	})

	return err
}

func (storage *SSHUser) reindex() error {
	oldIDIndex := storage.idIndex
	oldSSHPKeyIndex := storage.sshPKeyIndex

	storage.idIndex = make(map[model.ID]struct{})
	storage.sshPKeyIndex = make(map[string]model.ID)

	if err := storage.storage.initIndexes(sshUserDirName, storage.initIndexes); err != nil {
		storage.idIndex = oldIDIndex
		storage.sshPKeyIndex = oldSSHPKeyIndex

		return fmt.Errorf("unable to initialize SSH user indexes: %w", err)
	}

	return nil
}

func (storage *SSHUser) initIndexes(id model.ID) error {
	user, err := storage.readUser(id)
	if err != nil {
		return err
	}

	storage.initIndexesUser(user)
	return nil
}

func (storage *SSHUser) initIndexesUser(user *model.SSHUser) {
	storage.idIndex[user.ID] = struct{}{}
	storage.sshPKeyIndex[user.PKey] = user.ID
}

func (storage *SSHUser) dropIndexesUser(user *model.SSHUser) {
	delete(storage.idIndex, user.ID)
	delete(storage.sshPKeyIndex, user.PKey)
}

func (storage *SSHUser) readUser(id model.ID) (*model.SSHUser, error) {
	var user model.SSHUser

	if err := storage.storage.readFile(sshUserDirName, id, &user); err != nil {
		return nil, fmt.Errorf("unable to read SSH user %v: %w", id, err)
	}

	user.ID = id
	return &user, nil
}

func (storage *SSHUser) saveUser(user *model.SSHUser, newUser bool) error {
	createFile := noCreate

	if newUser {
		createFile = onlyCreate
	}

	if err := storage.storage.saveFile(sshUserDirName, user.ID, createFile, user); err != nil {
		return fmt.Errorf("unable to save SSH user %v: %w", user.ID, err)
	}

	return nil
}
