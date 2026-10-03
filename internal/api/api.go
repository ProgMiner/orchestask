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
	"bypm.ru/orchestask/internal/service"
	"bypm.ru/orchestask/internal/util"
)

type Server struct {
	service  *service.Service
	listener net.Listener
	mux      *http.ServeMux
}

func MakeAPIServer(service *service.Service, host string) (*Server, error) {
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("unable to listen %q: %w", host, err)
	}

	srv := &Server{service: service, listener: listener, mux: http.NewServeMux()}
	srv.mux.HandleFunc("GET /ssh-users", srv.listSSHUsers)
	srv.mux.HandleFunc("GET /ssh-users/pending", srv.pendingSSHUsers)
	srv.mux.HandleFunc("POST /ssh-users/{sshUserID}/approve", srv.approveSSHUser)
	srv.mux.HandleFunc("GET /users", srv.listUsers)
	srv.mux.HandleFunc("PUT /users/{userID}", srv.updateUser)
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
