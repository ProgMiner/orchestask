package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errInputEOF = errors.New("input closed")

type terminal struct {
	in  *bufio.Reader
	out io.Writer
}

func newTerminal() *terminal {
	return &terminal{in: bufio.NewReader(os.Stdin), out: os.Stdout}
}

func (term *terminal) readLine(prompt string) (string, error) {
	_, _ = fmt.Fprint(term.out, prompt)

	line, err := term.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	if errors.Is(err, io.EOF) && line == "" {
		return "", errInputEOF
	}

	return strings.TrimSpace(line), nil
}

func (term *terminal) printPending(users []sshUser) {
	if len(users) == 0 {
		fmt.Fprintln(term.out, "No pending SSH users.")
		return
	}

	for i, sshUser := range users {
		fmt.Fprintf(
			term.out,
			"[%d] %s  %s  %s\n",
			i+1,
			shortID(sshUser.ID),
			createdAt(sshUser.CreatedAt),
			keySummary(sshUser.PKey),
		)
	}
}

func (term *terminal) printSSHUsers(users []sshUser) {
	if len(users) == 0 {
		fmt.Fprintln(term.out, "No SSH users.")
		return
	}

	for i, sshUser := range users {
		status := "pending"
		if sshUser.Approved || sshUser.User != 0 {
			status = fmt.Sprintf("approved → user #%d", sshUser.User)
		}

		fmt.Fprintf(
			term.out,
			"[%d] %s  %s  %s  %s\n",
			i+1,
			shortID(sshUser.ID),
			createdAt(sshUser.CreatedAt),
			keySummary(sshUser.PKey),
			status,
		)
	}
}

func (term *terminal) printUsers(users []user) {
	if len(users) == 0 {
		fmt.Fprintln(term.out, "No users.")
		return
	}

	for i, item := range users {
		fmt.Fprintf(
			term.out,
			"[%d] %d %s (%s)\n",
			i+1,
			item.ID,
			displayName(item.FirstName, item.LastName),
			username(item.Username),
		)
	}
}

func sortedSSHUsers(users []sshUser) []sshUser {
	sorted := append([]sshUser(nil), users...)

	sort.SliceStable(sorted, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, sorted[i].CreatedAt)
		if leftErr != nil {
			return false
		}

		right, rightErr := time.Parse(time.RFC3339, sorted[j].CreatedAt)
		if rightErr != nil {
			return true
		}

		return left.After(right)
	})

	return sorted
}

func createdAt(value string) string {
	created, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "time unknown          "
	}

	return created.Local().Format("2006-01-02 15:04:05")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}

	return id
}

func keySummary(key string) string {
	fields := strings.Fields(key)
	if len(fields) < 2 {
		return key
	}

	body := fields[1]
	if len(body) > 20 {
		body = body[len(body)-20:]
	}

	result := "..." + body
	if len(fields) > 2 {
		result += " " + strings.Join(fields[2:], " ")
	}

	return result
}

func displayName(first, last string) string {
	name := strings.TrimSpace(first + " " + last)
	if name == "" {
		return "(unnamed)"
	}

	return name
}

func username(value string) string {
	if value == "" {
		return "no username"
	}

	return value
}

func (term *terminal) selectPending(users []sshUser) (sshUser, error) {
	for {
		value, err := term.readLine("Select a user (number or ID prefix, empty to cancel): ")
		if err != nil {
			return sshUser{}, err
		}

		if value == "" {
			return sshUser{}, nil
		}

		if index, err := strconv.Atoi(value); err == nil {
			if index >= 1 && index <= len(users) {
				return users[index-1], nil
			}

			fmt.Fprintf(term.out, "No pending SSH user at number %d.\n", index)
			continue
		}

		var matches []sshUser
		for _, item := range users {
			if strings.HasPrefix(strings.ToLower(item.ID), strings.ToLower(value)) {
				matches = append(matches, item)
			}
		}

		switch len(matches) {
		case 1:
			return matches[0], nil

		case 0:
			fmt.Fprintln(term.out, "No pending SSH user matches that ID prefix.")

		default:
			fmt.Fprintln(term.out, "ID prefix is ambiguous; candidates:")

			for _, item := range matches {
				fmt.Fprintf(term.out, "  %s\n", item.ID)
			}
		}
	}
}

func (term *terminal) selectUser(users []user, prompt string) (*user, error) {
	for {
		value, err := term.readLine(prompt)
		if err != nil {
			return nil, err
		}

		if value == "" {
			return nil, nil
		}

		if index, err := strconv.Atoi(value); err == nil && index >= 1 && index <= len(users) {
			return &users[index-1], nil
		}

		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			for i := range users {
				if users[i].ID == id {
					return &users[i], nil
				}
			}
		}

		fmt.Fprintln(term.out, "No user matches that selection; enter a list number or user ID.")
	}
}

func (term *terminal) askName(prompt, current string) (string, error) {
	value, err := term.readLine(prompt)
	if err != nil {
		return "", err
	}

	if value == "" {
		return current, nil
	}

	return value, nil
}

func (term *terminal) askYesNo(prompt string) (bool, error) {
	for {
		value, err := term.readLine(prompt)
		if err != nil {
			return false, err
		}

		switch strings.ToLower(value) {
		case "", "n", "no":
			return false, nil

		case "y", "yes":
			return true, nil

		default:
			fmt.Fprintln(term.out, "Please answer y or n.")
		}
	}
}
