package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/davidetoniatti/qoget/internal/config"
	"github.com/davidetoniatti/qoget/internal/history"
	"github.com/davidetoniatti/qoget/internal/naming"
	"github.com/davidetoniatti/qoget/internal/qobuz"
)

const loginHelp = `Usage: qoget login [-token TOKEN]

Stores the account token and checks it against Qobuz. Without -token, the token is asked
for on the terminal (it is not echoed).

Qobuz no longer accepts email and password from third-party clients (the web player moved
to OAuth with a captcha in April 2026), so the token is copied from a browser session:

  1. Log in at https://play.qobuz.com in Chrome or Firefox.
  2. Open the developer tools (F12 or Cmd-Opt-I), Network tab, and filter on "user/login".
  3. Reload https://play.qobuz.com/login and click the "login" request that appears.
  4. In its Response, copy the value of "user_auth_token".

The token is a JWT and expires; when downloads start failing with 401, repeat these steps.
`

// login is "qoget login": store the token and check it with user/get.
func (env *environment) login(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() { fmt.Fprint(env.stderr, loginHelp) }
	var token string
	fs.StringVar(&token, "token", "", "the account's user_auth_token (asked for if missing)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(env.configPath)
	if err != nil {
		return err
	}

	if token == "" {
		fmt.Fprint(env.stdout, "Paste the user_auth_token (see \"qoget login -h\" for where to find it): ")
		token, err = env.readSecret()
		if err != nil {
			return err
		}
		fmt.Fprintln(env.stdout)
	}
	token = strings.TrimSpace(strings.Trim(token, `"`))
	if token == "" {
		return errors.New("no token given")
	}

	client := qobuz.New(qobuz.Options{AuthToken: token, AppID: cfg.AppID, Secrets: cfg.Secrets})
	user, err := client.User(ctx)
	if err != nil {
		return fmt.Errorf("the token was refused: %w", err)
	}
	cfg.AuthToken = token
	if err := config.Save(env.configPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(env.stdout, "logged in as %s (%s, user id %s)\n", orText(user.DisplayName, user.Email), user.Email, user.ID)
	if user.Offer != "" {
		state := "active"
		if user.Canceled {
			state = "not renewing"
		}
		fmt.Fprintf(env.stdout, "plan: %s until %s (%s); %s\n", user.Offer, user.EndDate, state, user.Credential)
	}
	fmt.Fprintf(env.stdout, "token saved to %s\n", env.configPath)
	return nil
}

// readSecret reads one line without echo when stdin is a terminal, and plainly otherwise so
// a token can be piped in.
func (env *environment) readSecret() (string, error) {
	if f, ok := env.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		data, err := term.ReadPassword(int(f.Fd()))
		return string(data), err
	}
	line, err := bufio.NewReader(env.stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

const configHelp = `Usage: qoget config <show|path|set KEY VALUE|reset>

  show             print the settings (token and secrets hidden)
  path             print where the settings file is
  set KEY VALUE    change one setting
  reset            write the defaults, keeping the token

Keys: %s

Templates may use: %s
`

// configure is "qoget config".
func (env *environment) configure(args []string) error {
	help := fmt.Sprintf(configHelp, strings.Join(config.Keys(), ", "), strings.Join(naming.Names(), ", "))
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(env.stdout, help)
		return nil
	}
	cfg, err := config.Load(env.configPath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		data, err := json.MarshalIndent(cfg.Redacted(), "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(env.stdout, string(data))
		fmt.Fprintf(env.stdout, "\n%s\n", env.configPath)
		return nil
	case "path":
		fmt.Fprintln(env.stdout, env.configPath)
		return nil
	case "set":
		if len(args) != 3 {
			fmt.Fprint(env.stderr, help)
			return errors.New("config set takes a key and a value")
		}
		if err := cfg.Set(args[1], args[2]); err != nil {
			return err
		}
		if err := config.Save(env.configPath, cfg); err != nil {
			return err
		}
		fmt.Fprintf(env.stdout, "%s set\n", strings.ToLower(args[1]))
		return nil
	case "reset":
		fresh := config.Default()
		fresh.AuthToken = cfg.AuthToken
		if err := config.Save(env.configPath, fresh); err != nil {
			return err
		}
		fmt.Fprintf(env.stdout, "defaults written to %s (token kept)\n", env.configPath)
		return nil
	}
	fmt.Fprint(env.stderr, help)
	return fmt.Errorf("unknown config command %q", args[0])
}

// history is "qoget history": what has been downloaded, or forget it all.
func (env *environment) history(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() {
		fmt.Fprintln(env.stderr, "Usage: qoget history [-purge]\n\nLists what has been downloaded (kind:id per line); -purge forgets it all.")
	}
	var purge bool
	fs.BoolVar(&purge, "purge", false, "delete the history, so everything may be downloaded again")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := config.HistoryPath()
	if purge {
		if err := history.Purge(path); err != nil {
			return err
		}
		fmt.Fprintf(env.stdout, "history purged (%s)\n", path)
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(env.stdout, "nothing downloaded yet (%s)\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprint(env.stdout, string(data))
	return nil
}
