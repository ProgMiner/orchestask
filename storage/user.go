package storage

import (
	"fmt"
)

import (
	"bypm.ru/orchestask/model"
)

type SSHUsernamePKey struct {
	Username string
	PKey     string
}

type User struct {
	storage              *Storage
	idIndex              map[model.ID]struct{}
	sshUsernamePKeyIndex map[SSHUsernamePKey]model.ID
	tgLinkIndex          map[string]model.ID
}

const (
	userDirName = "user"
)

func (storage *Storage) User() (*User, error) {
	user := &User{
		storage:              storage,
		idIndex:              make(map[model.ID]struct{}),
		sshUsernamePKeyIndex: make(map[SSHUsernamePKey]model.ID),
		tgLinkIndex:          make(map[string]model.ID),
	}

	if err := storage.initIndexes(userDirName, user.initIndexes); err != nil {
		return nil, fmt.Errorf("unable to initialize indexes: %w", err)
	}

	return user, nil
}

func (storage *User) ExistsByID(id model.ID) (bool, error) {
	_, ok := storage.idIndex[id]
	return ok, nil
}

func (storage *User) FindByID(id model.ID) (*model.User, error) {
	if _, ok := storage.idIndex[id]; !ok {
		return nil, nil
	}

	return storage.readUser(id)
}

func (storage *User) FindByUsernameAndPKey(username, pkey string) (*model.User, error) {
	id, ok := storage.sshUsernamePKeyIndex[SSHUsernamePKey{username, pkey}]
	if !ok {
		return nil, nil
	}

	return storage.readUser(id)
}

func (storage *User) FindByTGLink(tgLink string) (*model.User, error) {
	id, ok := storage.tgLinkIndex[tgLink]
	if !ok {
		return nil, nil
	}

	return storage.readUser(id)
}

func (storage *User) Save(user *model.User) (*model.User, error) {
	if user.ID == "" {
		user.ID = model.MakeID(fmt.Sprintf("%s-%s.json", user.SSHUsername, user.SSHPKey))
	}

	if oldUser, _ := storage.readUser(user.ID); oldUser != nil {
		storage.dropIndexesUser(oldUser)
	}

	if err := storage.saveUser(user); err != nil {
		return nil, err
	}

	storage.initIndexesUser(user)
	return user, nil
}

func (storage *User) initIndexes(id model.ID) error {
	user, err := storage.readUser(id)
	if err != nil {
		return err
	}

	storage.initIndexesUser(user)
	return nil
}

func (storage *User) initIndexesUser(user *model.User) {
	storage.idIndex[user.ID] = struct{}{}
	storage.sshUsernamePKeyIndex[SSHUsernamePKey{user.SSHUsername, user.SSHPKey}] = user.ID

	if user.TGLink != "" {
		storage.tgLinkIndex[user.TGLink] = user.ID
	}
}

func (storage *User) dropIndexesUser(user *model.User) {
	delete(storage.idIndex, user.ID)
	delete(storage.sshUsernamePKeyIndex, SSHUsernamePKey{user.SSHUsername, user.SSHPKey})

	if user.TGLink != "" {
		delete(storage.tgLinkIndex, user.TGLink)
	}
}

func (storage *User) readUser(id model.ID) (*model.User, error) {
	var user model.User

	if err := storage.storage.readFile(userDirName, id, &user); err != nil {
		return nil, fmt.Errorf("unable to read user %v: %w", id, err)
	}

	user.ID = id
	return &user, nil
}

func (storage *User) saveUser(user *model.User) error {
	if err := storage.storage.saveFile(userDirName, user.ID, user); err != nil {
		return fmt.Errorf("unable to save user %v: %w", user.ID, err)
	}

	return nil
}
