package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/service"
	"bypm.ru/orchestask/internal/util"
)

type Server struct {
	service  *service.Service
	listener net.Listener
	mux      *http.ServeMux
}

type sshUserResponse struct {
	ID       string `json:"id"`
	PKey     string `json:"pkey"`
	Approved bool   `json:"approved"`
}

func MakeAPIServer(service *service.Service, host string) (*Server, error) {
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("unable to listen %q: %w", host, err)
	}

	srv := &Server{service: service, listener: listener, mux: http.NewServeMux()}
	srv.mux.HandleFunc("GET /users", srv.listUsers)
	srv.mux.HandleFunc("GET /users/pending", srv.pendingUsers)
	srv.mux.HandleFunc("POST /users/{id}/approve", srv.approveUser)
	return srv, nil
}

func (srv *Server) Run(ctx context.Context) error {
	ctx = util.WithLoggingScope(ctx, "API")
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			util.Log(ctx, "%s %s", r.Method, r.URL.Path)
			srv.mux.ServeHTTP(w, r)
		}),
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	fmt.Printf("API server started on %s\n", srv.listener.Addr().String())

	if err := server.Serve(srv.listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (srv *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := srv.service.User.ListSSHUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	response, err := srv.makeSSHUsersResponse(users)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (srv *Server) pendingUsers(w http.ResponseWriter, r *http.Request) {
	users, err := srv.service.User.ListSSHUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	response, err := srv.makeSSHUsersResponse(users)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	pending := make([]sshUserResponse, 0, len(response))
	for _, user := range response {
		if !user.Approved {
			pending = append(pending, user)
		}
	}

	writeJSON(w, http.StatusOK, pending)
}

func (srv *Server) makeSSHUsersResponse(users []*model.SSHUser) ([]sshUserResponse, error) {
	response := make([]sshUserResponse, 0, len(users))

	for _, user := range users {
		id, err := model.IDToString(user.ID)
		if err != nil {
			return nil, err
		}

		response = append(response, sshUserResponse{ID: id, PKey: user.PKey, Approved: user.TG != 0})
	}

	return response, nil
}

func (srv *Server) approveUser(w http.ResponseWriter, r *http.Request) {
	id := model.StringToID(r.PathValue("id"))
	_, err := srv.service.User.Approve(id)

	if errors.Is(err, service.ErrNoUser) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	if errors.Is(err, service.ErrAlreadyApproved) {
		writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "approved": true})
		return
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "approved": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
