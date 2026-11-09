package github

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Login struct {
	ClientID              string    `json:"client_id"`
	AccessToken           string    `json:"access_token"`
	ExpiresAt             time.Time `json:"expires_at,omitzero"`
	RefreshToken          string    `json:"refresh_token,omitempty"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at,omitzero"`
}

func (l Login) Expires() bool { return !l.ExpiresAt.IsZero() }

func (l Login) CanRefresh(now time.Time) bool {
	return l.RefreshToken != "" && (l.RefreshTokenExpiresAt.IsZero() || now.Before(l.RefreshTokenExpiresAt))
}

func (l Login) Usable(now time.Time) bool {
	return !l.Expires() || now.Before(l.ExpiresAt) || l.CanRefresh(now)
}

type Store struct{ Path string }

func (s Store) lockPath() string { return filepath.Join(filepath.Dir(s.Path), "app.lock") }

func (s Store) Read() (Login, error) {
	info, statErr := os.Stat(s.Path)
	if errors.Is(statErr, fs.ErrNotExist) {
		return Login{}, errorf("not logged in to the GitHub App (no %s); %s", s.Path, relogin)
	}
	content, err := os.ReadFile(s.Path)
	if statErr != nil || err != nil {
		return Login{}, errorf("cannot read %s: %v", s.Path, errors.Join(statErr, err))
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Login{}, errorf("%s is readable by other users (mode %o); run chmod 600 on it", s.Path, info.Mode().Perm())
	}
	var login Login
	if err := json.Unmarshal(content, &login); err != nil || login.AccessToken == "" || login.ClientID == "" {
		return Login{}, errorf("%s is not a GitHub App login stage can read; %s", s.Path, relogin)
	}
	return login, nil
}

func (s Store) Write(login Login) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(login, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".app-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(append(content, '\n'))
	if err := errors.Join(writeErr, temporary.Chmod(0o600), temporary.Sync(), temporary.Close()); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), s.Path)
}

func (s Store) Remove() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s Store) Lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(s.lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, errorf("cannot lock %s: %v", s.lockPath(), err)
	}
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

const RefreshMargin = 40 * time.Minute

type Token struct {
	Login         Login
	Installations []Installation
	Checked       bool
	Refreshed     bool
}

func (c *Client) Token(store Store, clientID string, check bool) (Token, error) {
	unlock, err := store.Lock()
	if err != nil {
		return Token{}, err
	}
	defer unlock()
	login, err := store.Read()
	if err != nil {
		return Token{}, err
	}
	if login.ClientID != clientID {
		return Token{}, errorf("%s holds a login for GitHub App client ID %s, but [github] app_client_id is %s; %s",
			store.Path, login.ClientID, clientID, relogin)
	}
	now := c.Now()
	result := Token{Login: login}
	if login.Expires() && !now.Add(RefreshMargin).Before(login.ExpiresAt) {
		if !login.CanRefresh(now) {
			return Token{}, errorf("the GitHub App login in %s expired; %s", store.Path, relogin)
		}
		refreshed, err := c.Refresh(login)
		if err != nil {
			return Token{}, err
		}
		if err := store.Write(refreshed); err != nil {
			return Token{}, err
		}
		result.Login, result.Refreshed = refreshed, true
		check = true
	}
	if !check {
		return result, nil
	}
	installations, err := c.Installations(result.Login.AccessToken)
	if err != nil {
		return Token{}, err
	}
	if err := CheckReadOnly(installations); err != nil {
		return Token{}, errors.Join(err, store.Remove())
	}
	result.Installations, result.Checked = installations, true
	return result, nil
}

func (c *Client) Login(store Store, clientID string, prompt func(DeviceCode)) (Token, error) {
	code, err := c.RequestDeviceCode(clientID)
	if err != nil {
		return Token{}, err
	}
	prompt(code)
	login, err := c.PollDeviceToken(clientID, code)
	if err != nil {
		return Token{}, err
	}
	installations, err := c.Installations(login.AccessToken)
	if err != nil {
		return Token{}, err
	}
	unlock, err := store.Lock()
	if err != nil {
		return Token{}, err
	}
	defer unlock()
	if err := CheckReadOnly(installations); err != nil {
		return Token{}, errors.Join(err, store.Remove())
	}
	if err := store.Write(login); err != nil {
		return Token{}, err
	}
	return Token{Login: login, Installations: installations, Checked: true}, nil
}
