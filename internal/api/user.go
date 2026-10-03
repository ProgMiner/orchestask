package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/service"
)

type userDTO struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
}

func (srv *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := srv.service.User.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	response := make([]userDTO, 0, len(users))
	for _, user := range users {
		response = append(response, makeUserDTO(user))
	}
	writeJSON(w, http.StatusOK, response)
}

func (srv *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || userID == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	var body struct {
		FirstName *string `json:"firstName"`
		LastName  *string `json:"lastName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	user, err := srv.service.User.GetByID(userID)
	if errors.Is(err, service.ErrNoUser) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	firstName, lastName := user.FirstName, user.LastName
	if body.FirstName != nil {
		firstName = *body.FirstName
	}
	if body.LastName != nil {
		lastName = *body.LastName
	}

	user, err = srv.service.User.UpdateName(userID, firstName, lastName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, makeUserDTO(user))
}

func makeUserDTO(user *model.User) userDTO {
	return userDTO{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}
}
