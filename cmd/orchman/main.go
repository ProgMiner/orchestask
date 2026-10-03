package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

import (
	"bypm.ru/orchestask/internal/util"
)

const (
	okExit = iota
	flagParseExit
	connectionExit
)

const (
	serverDefault = "127.0.0.1:8080"
	serverEnv     = "HTTP_HOST"
)

var server = flag.String("server", util.GetenvOr(serverEnv, serverDefault), "REST API server address")

type application struct {
	client *apiClient
	term   *terminal
}

func parseArgs() error {
	cmdName := ""
	if len(os.Args) > 0 {
		cmdName = os.Args[0]
	}

	flag.CommandLine.Init(cmdName, flag.ContinueOnError)
	return flag.CommandLine.Parse(os.Args[1:])
}

func (app *application) run() error {
	firstMenu := true

	for {
		pending, err := app.client.getPending()
		if err != nil {
			if firstMenu {
				return fmt.Errorf("unable to connect to API server: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Unable to refresh pending approvals: %v\n", err)
			fmt.Fprintln(app.term.out, "Pending approvals: unknown")
		} else {
			fmt.Fprintf(app.term.out, "Pending approvals: %d\n", len(pending))
		}

		firstMenu = false

		fmt.Fprintln(app.term.out, "  [a]pprove a pending SSH user")
		fmt.Fprintln(app.term.out, "  [p] refresh pending list")
		fmt.Fprintln(app.term.out, "  [l] list all SSH users")
		fmt.Fprintln(app.term.out, "  [u] list users")
		fmt.Fprintln(app.term.out, "  [n] rename a user")
		fmt.Fprintln(app.term.out, "  [q]uit")

		choice, err := app.term.readLine("> ")
		if errors.Is(err, errInputEOF) {
			return nil
		}

		if err != nil {
			return err
		}

		switch choice {
		case "a":
			err = app.approve()

		case "p":
			err = app.listPending()

		case "l":
			err = app.listSSHUsers()

		case "u":
			err = app.listUsers()

		case "n":
			err = app.renameUser()

		case "q":
			return nil

		default:
			fmt.Fprintln(app.term.out, "Unknown command; choose a, p, l, u, n, or q.")
		}

		if errors.Is(err, errInputEOF) {
			return nil
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
}

func (app *application) listPending() error {
	users, err := app.client.getPending()
	if err != nil {
		return err
	}

	app.term.printPending(sortedSSHUsers(users))
	return nil
}

func (app *application) listSSHUsers() error {
	users, err := app.client.getAllSSHUsers()
	if err != nil {
		return err
	}

	users = sortedSSHUsers(users)
	app.term.printSSHUsers(users)
	return nil
}

func (app *application) listUsers() error {
	users, err := app.client.getUsers()
	if err != nil {
		return err
	}

	app.term.printUsers(users)
	return nil
}

func (app *application) approve() error {
	users, err := app.client.getPending()
	if err != nil {
		return err
	}

	if len(users) == 0 {
		fmt.Fprintln(app.term.out, "No pending SSH users.")
		return nil
	}

	users = sortedSSHUsers(users)
	app.term.printPending(users)

	sshUser, err := app.term.selectPending(users)
	if err != nil || sshUser.ID == "" {
		return err
	}

	attach, err := app.term.askYesNo("Attach to an existing user? [y/N]: ")
	if err != nil {
		return err
	}

	body := approveBody{}
	if attach {
		available, err := app.client.getUsers()
		if err != nil {
			return err
		}

		if len(available) == 0 {
			fmt.Fprintln(app.term.out, "No users.")
			return nil
		}

		app.term.printUsers(available)
		selected, err := app.term.selectUser(available, "Select a user (number or ID, empty to cancel): ")
		if err != nil || selected == nil {
			return err
		}

		body.User = &selected.ID
	} else {
		body.FirstName, err = app.term.askName("First name (optional): ", "")
		if err != nil {
			return err
		}

		body.LastName, err = app.term.askName("Last name (optional): ", "")
		if err != nil {
			return err
		}
	}

	result, err := app.client.approveSSHUser(sshUser.ID, body)
	if err != nil {
		return err
	}

	fmt.Fprintf(app.term.out, "Approved: ssh user %s → user #%d %s\n", result.ID, result.User, displayName(result.FirstName, result.LastName))
	return nil
}

func (app *application) renameUser() error {
	users, err := app.client.getUsers()
	if err != nil {
		return err
	}

	if len(users) == 0 {
		fmt.Fprintln(app.term.out, "No users.")
		return nil
	}

	app.term.printUsers(users)
	selected, err := app.term.selectUser(users, "Select a user (number or ID, empty to cancel): ")
	if err != nil || selected == nil {
		return err
	}

	firstName, err := app.term.askName(
		fmt.Sprintf("First name (press Enter to keep current %q): ", selected.FirstName),
		selected.FirstName,
	)
	if err != nil {
		return err
	}

	lastName, err := app.term.askName(
		fmt.Sprintf("Last name (press Enter to keep current %q): ", selected.LastName),
		selected.LastName,
	)
	if err != nil {
		return err
	}

	updated, err := app.client.updateUser(selected.ID, firstName, lastName)
	if err != nil {
		return err
	}

	fmt.Fprintf(
		app.term.out,
		"Updated user #%d: %s\n",
		updated.ID,
		displayName(updated.FirstName, updated.LastName),
	)

	return nil
}

func _main() (int, string) {
	if err := parseArgs(); err != nil {
		if err == flag.ErrHelp {
			return okExit, ""
		}
		return flagParseExit, err.Error()
	}

	client, err := newAPIClient(*server)
	if err != nil {
		return flagParseExit, err.Error()
	}

	app := application{client: client, term: newTerminal()}
	if err := app.run(); err != nil {
		return connectionExit, err.Error()
	}

	return okExit, ""
}

func main() {
	code, message := _main()
	if message != "" {
		fmt.Fprintln(os.Stderr, message)
	}

	if code != okExit {
		os.Exit(code)
	}
}
