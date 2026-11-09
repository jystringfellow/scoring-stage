package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

const relogin = "run `stage github login`"

type Client struct {
	HTTP   *http.Client
	WebURL string
	APIURL string
	Now    func() time.Time
	Sleep  func(time.Duration)
}

func NewClient() *Client {
	return &Client{
		HTTP:   &http.Client{Timeout: 30 * time.Second},
		WebURL: "https://github.com",
		APIURL: "https://api.github.com",
		Now:    time.Now,
		Sleep:  time.Sleep,
	}
}

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type oauthResponse struct {
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
	Interval              int    `json:"interval"`
	AccessToken           string `json:"access_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	TokenType             string `json:"token_type"`
}

func (c *Client) post(path string, form url.Values, into any) error {
	request, err := http.NewRequest(http.MethodPost, c.WebURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	return c.do(request, into)
}

func (c *Client) do(request *http.Request, into any) error {
	response, err := c.HTTP.Do(request)
	if err != nil {
		return errorf("cannot reach GitHub: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return errorf("cannot read GitHub's answer to %s %s: %v", request.Method, request.URL.Path, err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return errorf("GitHub refused the app token for %s (401 Unauthorized); %s", request.URL.Path, relogin)
	}
	if response.StatusCode != http.StatusOK {
		return errorf("GitHub answered %s %s with %s", request.Method, request.URL.Path, response.Status)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return errorf("cannot read GitHub's answer to %s %s: %v", request.Method, request.URL.Path, err)
	}
	return nil
}

func oauthError(response oauthResponse) error {
	switch response.Error {
	case "device_flow_disabled":
		return errorf("the GitHub App does not allow the device flow; turn on Enable Device Flow in its settings, then %s", relogin)
	case "incorrect_client_credentials":
		return errorf("GitHub does not know the client ID in [github] app_client_id; copy the Client ID from the GitHub App's settings page")
	case "bad_refresh_token":
		return errorf("GitHub refused the stored refresh token (it expired or was revoked); %s", relogin)
	}
	message := "GitHub refused the request: " + response.Error
	if response.ErrorDescription != "" {
		message += " (" + response.ErrorDescription + ")"
	}
	return &Error{message}
}

func (c *Client) RequestDeviceCode(clientID string) (DeviceCode, error) {
	var answer struct {
		DeviceCode
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := c.post("/login/device/code", url.Values{"client_id": {clientID}}, &answer); err != nil {
		return DeviceCode{}, err
	}
	if answer.Error != "" {
		return DeviceCode{}, oauthError(oauthResponse{Error: answer.Error, ErrorDescription: answer.ErrorDescription})
	}
	if answer.DeviceCode.DeviceCode == "" || answer.UserCode == "" || answer.VerificationURI == "" {
		return DeviceCode{}, errorf("GitHub's device code answer has no device code, user code or verification URI")
	}
	return answer.DeviceCode, nil
}

const deviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

func (c *Client) PollDeviceToken(clientID string, code DeviceCode) (Login, error) {
	interval := time.Duration(max(code.Interval, 1)) * time.Second
	deadline := c.Now().Add(time.Duration(max(code.ExpiresIn, 1)) * time.Second)
	for {
		c.Sleep(interval)
		var answer oauthResponse
		err := c.post("/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {code.DeviceCode},
			"grant_type":  {deviceGrant},
		}, &answer)
		if err != nil {
			return Login{}, err
		}
		switch answer.Error {
		case "":
			return c.loginFrom(clientID, answer)
		case "authorization_pending":
		case "slow_down":
			if answer.Interval > 0 {
				interval = time.Duration(answer.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "expired_token":
			return Login{}, errorf("the code %s expired before it was entered; %s again", code.UserCode, relogin)
		case "access_denied":
			return Login{}, errorf("the authorization was cancelled on GitHub; %s again to retry", relogin)
		default:
			return Login{}, oauthError(answer)
		}
		if !c.Now().Before(deadline) {
			return Login{}, errorf("the code %s expired before it was entered; %s again", code.UserCode, relogin)
		}
	}
}

func (c *Client) Refresh(login Login) (Login, error) {
	var answer oauthResponse
	err := c.post("/login/oauth/access_token", url.Values{
		"client_id":     {login.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {login.RefreshToken},
	}, &answer)
	if err != nil {
		return Login{}, err
	}
	if answer.Error != "" {
		return Login{}, oauthError(answer)
	}
	return c.loginFrom(login.ClientID, answer)
}

func (c *Client) loginFrom(clientID string, answer oauthResponse) (Login, error) {
	if answer.AccessToken == "" {
		return Login{}, errorf("GitHub's token answer has no access token")
	}
	if !strings.HasPrefix(answer.AccessToken, "ghu_") {
		return Login{}, errorf("GitHub returned a token that is not a GitHub App user token (ghu_); check that [github] app_client_id names a GitHub App, not an OAuth app")
	}
	now := c.Now().UTC().Truncate(time.Second)
	login := Login{ClientID: clientID, AccessToken: answer.AccessToken, RefreshToken: answer.RefreshToken}
	if answer.ExpiresIn > 0 {
		login.ExpiresAt = now.Add(time.Duration(answer.ExpiresIn) * time.Second)
	}
	if answer.RefreshToken != "" && answer.RefreshTokenExpiresIn > 0 {
		login.RefreshTokenExpiresAt = now.Add(time.Duration(answer.RefreshTokenExpiresIn) * time.Second)
	}
	return login, nil
}

type Installation struct {
	Account     string
	Permissions map[string]string
}

func (c *Client) Installations(token string) ([]Installation, error) {
	var installations []Installation
	for page := 1; ; page++ {
		request, err := http.NewRequest(http.MethodGet, c.APIURL+"/user/installations?per_page=100&page="+strconv.Itoa(page), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		var answer struct {
			TotalCount    *int `json:"total_count"`
			Installations []struct {
				Account struct {
					Login string `json:"login"`
				} `json:"account"`
				Permissions map[string]string `json:"permissions"`
			} `json:"installations"`
		}
		if err := c.do(request, &answer); err != nil {
			return nil, err
		}
		if answer.TotalCount == nil {
			return nil, errorf("cannot read GitHub's list of the app's installations: it has no total_count")
		}
		for _, entry := range answer.Installations {
			installations = append(installations, Installation{Account: entry.Account.Login, Permissions: entry.Permissions})
		}
		if len(answer.Installations) == 0 || len(installations) >= *answer.TotalCount {
			return installations, nil
		}
	}
}

func Accounts(installations []Installation) []string {
	accounts := make([]string, 0, len(installations))
	for _, installation := range installations {
		accounts = append(accounts, installation.Account)
	}
	sort.Strings(accounts)
	return accounts
}

var ErrCanWrite = errors.New("the GitHub App can write")

func CheckReadOnly(installations []Installation) error {
	var problems []string
	for _, installation := range installations {
		var granted []string
		for permission, level := range installation.Permissions {
			if level != "read" {
				granted = append(granted, permission+": "+level)
			}
		}
		if len(granted) > 0 {
			sort.Strings(granted)
			problems = append(problems, fmt.Sprintf("the installation on %s grants %s", installation.Account, strings.Join(granted, ", ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%w: %s. stage keeps no login for an app that can write. Make every permission read-only in the app's settings, "+
		"have each installation accept the change, then %s", ErrCanWrite, strings.Join(problems, "; "), relogin)
}
