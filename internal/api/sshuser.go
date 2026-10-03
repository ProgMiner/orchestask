package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/service"
)

type sshUserDTO struct {
	ID        string `json:"id"`
	PKey      string `json:"pkey"`
	CreatedAt string `json:"createdAt,omitempty"`
	Approved  bool   `json:"approved"`
	User      int64  `json:"user,omitempty"`
}

func (srv *Server) listSSHUsers(w http.ResponseWriter, r *http.Request) {
	users, err := srv.service.User.ListSSHUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	response, err := makeSSHUserDTOs(users)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (srv *Server) pendingSSHUsers(w http.ResponseWriter, r *http.Request) {
	users, err := srv.service.User.ListSSHUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	pending := make([]*model.SSHUser, 0, len(users))
	for _, user := range users {
		if user.TG == 0 {
			pending = append(pending, user)
		}
	}

	response, err := makeSSHUserDTOs(pending)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func makeSSHUserDTOs(users []*model.SSHUser) ([]sshUserDTO, error) {
	response := make([]sshUserDTO, 0, len(users))
	for _, user := range users {
		id, err := model.IDToString(user.ID)
		if err != nil {
			return nil, err
		}

		item := sshUserDTO{ID: id, PKey: user.PKey, Approved: user.TG != 0}
		if !user.CreatedAt.IsZero() {
			item.CreatedAt = user.CreatedAt.Format(time.RFC3339)
		}
		if user.TG != 0 {
			item.User = user.TG
		}
		response = append(response, item)
	}

	return response, nil
}

func (srv *Server) approveSSHUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User      *int64 `json:"user"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
	}

	sshID := model.StringToID(r.PathValue("sshUserID"))
	var user *model.User
	var err error
	if body.User != nil {
		user, err = srv.service.User.ApproveExisting(sshID, *body.User, body.FirstName, body.LastName)
	} else {
		user, err = srv.service.User.Approve(sshID, body.FirstName, body.LastName)
	}

	if errors.Is(err, service.ErrAlreadyApproved) {
		sshUser, getErr := srv.service.User.GetSSHUser(sshID)
		if getErr != nil {
			err = getErr
		} else if sshUser == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "SSH user not found"})
			return
		} else {
			user, err = srv.service.User.GetByID(sshUser.TG)
		}
	}

	if errors.Is(err, service.ErrNoUser) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        r.PathValue("sshUserID"),
		"user":      user.ID,
		"firstName": user.FirstName,
		"lastName":  user.LastName,
	})
}
