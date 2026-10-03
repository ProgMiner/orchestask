package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type apiClient struct {
	baseURL string
	http    *http.Client
}

type apiError struct {
	Code int
	Msg  string
}

func (err *apiError) Error() string {
	return fmt.Sprintf("server returned HTTP %d: %s", err.Code, err.Msg)
}

type sshUser struct {
	ID        string `json:"id"`
	PKey      string `json:"pkey"`
	CreatedAt string `json:"createdAt"`
	Approved  bool   `json:"approved"`
	User      int64  `json:"user"`
}

type user struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type approveBody struct {
	User      *int64 `json:"user,omitempty"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type approveResult struct {
	ID        string `json:"id"`
	User      int64  `json:"user"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

func newAPIClient(server string) (*apiClient, error) {
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}

	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid server URL %q", server)
	}

	return &apiClient{
		baseURL: strings.TrimRight(server, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (client *apiClient) getPending() ([]sshUser, error) {
	var users []sshUser
	err := client.request(http.MethodGet, "/ssh-users/pending", nil, &users)
	return users, err
}

func (client *apiClient) getAllSSHUsers() ([]sshUser, error) {
	var users []sshUser
	err := client.request(http.MethodGet, "/ssh-users", nil, &users)
	return users, err
}

func (client *apiClient) getUsers() ([]user, error) {
	var users []user
	err := client.request(http.MethodGet, "/users", nil, &users)
	return users, err
}

func (client *apiClient) approveSSHUser(id string, body approveBody) (approveResult, error) {
	var result approveResult
	err := client.request(http.MethodPost, "/ssh-users/"+url.PathEscape(id)+"/approve", body, &result)
	return result, err
}

func (client *apiClient) updateUser(id int64, firstName, lastName string) (user, error) {
	var result user
	err := client.request(http.MethodPut, fmt.Sprintf("/users/%d", id), struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	}{FirstName: firstName, LastName: lastName}, &result)

	return result, err
}

func (client *apiClient) request(method, path string, body, result any) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, client.baseURL+path, requestBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var message struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
			message.Error = http.StatusText(resp.StatusCode)
		}
		if message.Error == "" {
			message.Error = http.StatusText(resp.StatusCode)
		}
		return &apiError{Code: resp.StatusCode, Msg: message.Error}
	}

	if result == nil {
		_, err := io.Copy(io.Discard, resp.Body)
		return err
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
