package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
)

type User struct {
	storage *storage.Storage

	tgWaiters      map[model.ID]chan struct{}
	tgWaitersMutex sync.Mutex
}

var (
	ErrNoUser        = errors.New("user not found")
	ErrSSHUserHaveTG = errors.New("user have already attached TG")
)

func NewUser(storage *storage.Storage) (*User, error) {
	service := &User{
		storage:   storage,
		tgWaiters: make(map[model.ID]chan struct{}),
	}

	return service, nil
}

func (service *User) Authenticate(pkey string) (*model.SSHUser, error) {
	return service.storage.SSHUser.FindByPKey(pkey)
}

func (service *User) Register(pkey string) (*model.SSHUser, error) {
	return service.storage.SSHUser.Save(&model.SSHUser{PKey: pkey})
}

func (service *User) GetByID(id int64) (*model.User, error) {
	user, err := service.storage.User.FindByID(id)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, ErrNoUser
	}

	return user, nil
}

func (service *User) GetAll() ([]*model.User, error) {
	return service.storage.User.FindAll()
}

func (service *User) UpdateContainer(id int64, image, container string) (*model.User, error) {
	user, err := service.GetByID(id)
	if err != nil {
		return nil, err
	}

	user.ContainerImage = image
	user.Container = container

	return service.storage.User.Save(user)
}

func (service *User) WaitTGAttached(ctx context.Context, id model.ID) (*model.SSHUser, error) {
	if ok, err := service.storage.SSHUser.ExistsByID(id); !ok || err != nil {
		if err == nil {
			err = ErrNoUser
		}

		return nil, err
	}

	waiter, _ := util.Synchronized(&service.tgWaitersMutex, func() (<-chan struct{}, struct{}) {
		waiter, ok := service.tgWaiters[id]

		if !ok {
			waiter = make(chan struct{})
			service.tgWaiters[id] = waiter
		}

		return waiter, struct{}{}
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-waiter:
		return service.storage.SSHUser.FindByID(id)
	}
}

func (service *User) AttachTG(
	tgLink string,
	tg int64,
	username, firstName, lastName string,
) (*model.User, error) {
	sshUser, err := service.storage.SSHUser.FindByID(model.StringToID(tgLink))
	if err != nil {
		return nil, err
	}

	if sshUser == nil {
		return nil, ErrNoUser
	}

	if sshUser.TG != 0 {
		return nil, ErrSSHUserHaveTG
	}

	user, err := service.storage.User.FindByID(tg)
	if err != nil {
		return nil, err
	}

	if user == nil {
		user = &model.User{
			ID:        tg,
			Username:  username,
			FirstName: firstName,
			LastName:  lastName,
		}

		user, err = service.storage.User.Save(user)
		if err != nil {
			return nil, fmt.Errorf("unable to create user for TG %d: %w", tg, err)
		}
	}

	sshUser.TG = tg
	sshUser, err = service.storage.SSHUser.Save(sshUser)
	if err != nil {
		return nil, err
	}

	waiter, _ := util.Synchronized(&service.tgWaitersMutex, func() (chan struct{}, struct{}) {
		waiter, ok := service.tgWaiters[sshUser.ID]

		if ok {
			delete(service.tgWaiters, sshUser.ID)
		}

		return waiter, struct{}{}
	})

	if waiter != nil {
		close(waiter)
	}

	return user, nil
}
