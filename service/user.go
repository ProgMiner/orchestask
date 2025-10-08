package service

import (
	"context"
	"errors"
	"fmt"
)

import (
	"github.com/google/uuid"
)

import (
	"bypm.ru/orchestask/model"
	"bypm.ru/orchestask/storage"
)

type User struct {
	storage *storage.User

	tgWaiters map[model.ID]chan struct{}
}

var (
	NoUserErr     = errors.New("user not found")
	UserHaveTGErr = errors.New("user have already attached TG")
)

func NewUser(storage *storage.Storage) (*User, error) {
	userStorage, err := storage.User()
	if err != nil {
		return nil, fmt.Errorf("unable to init user storage: %w", err)
	}

	service := &User{
		storage:   userStorage,
		tgWaiters: make(map[model.ID]chan struct{}),
	}

	return service, nil
}

func (service *User) Identify(username, pkey string) (*model.User, error) {
	return service.storage.FindByUsernameAndPKey(username, pkey)
}

func (service *User) Register(username, pkey string) (*model.User, error) {
	user, err := service.storage.FindByUsernameAndPKey(username, pkey)
	if err != nil {
		return nil, err
	}

	if user != nil {
		return user, fmt.Errorf("user is already exists")
	}

	return service.storage.Save(&model.User{SSHUsername: username, SSHPKey: pkey})
}

func (service *User) UpdateContainer(id model.ID, image, container string) (*model.User, error) {
	user, err := service.storage.FindByID(id)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, NoUserErr
	}

	user.ContainerImage = image
	user.Container = container

	return service.storage.Save(user)
}

func (service *User) UpdateTGLink(id model.ID) (*model.User, error) {
	user, err := service.storage.FindByID(id)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, NoUserErr
	}

	user.TGLink = uuid.NewString()
	return service.storage.Save(user)
}

func (service *User) WaitTGAttached(ctx context.Context, id model.ID) (*model.User, error) {
	ok, err := service.storage.ExistsByID(id)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, NoUserErr
	}

	waiter, ok := service.tgWaiters[id]
	if !ok {
		waiter = make(chan struct{})
		service.tgWaiters[id] = waiter
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-waiter:
		return service.storage.FindByID(id)
	}
}

func (service *User) AttachTG(
	tgLink string,
	tg int64,
	tgUsername, tgFirstName, tgLastName string,
) (*model.User, error) {
	user, err := service.storage.FindByTGLink(tgLink)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, NoUserErr
	}

	if user.TG != 0 {
		return nil, UserHaveTGErr
	}

	user.TG = tg
	user.TGUsername = tgUsername
	user.TGFirstName = tgFirstName
	user.TGLastName = tgLastName

	user, err = service.storage.Save(user)
	if err != nil {
		return nil, err
	}

	if waiter, ok := service.tgWaiters[user.ID]; ok {
		close(waiter)
		delete(service.tgWaiters, user.ID)
	}

	return user, nil
}
