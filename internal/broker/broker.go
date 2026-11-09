package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Port   = 9911
	Prefix = "/credential-process/"
)

var Address = "127.0.0.1:" + strconv.Itoa(Port)

type Error struct {
	Status int
	msg    string
}

func (e *Error) Error() string { return e.msg }

type Result struct {
	Stdout, Stderr string
	ExitCode       int
}

type Runner func(ctx context.Context, argv []string) (Result, error)

func ExecRunner(ctx context.Context, argv []string) (Result, error) {
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("timed out: %w", ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return Result{stdout.String(), stderr.String(), exitErr.ExitCode()}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{stdout.String(), stderr.String(), 0}, nil
}

type Credentials interface {
	CredentialProcess(profile string, granted map[string]bool) (json.RawMessage, error)
}

type CredentialSource struct {
	Run     Runner
	AWS     string
	Timeout time.Duration
}

func NewCredentialSource(run Runner) *CredentialSource {
	return &CredentialSource{Run: run, AWS: "aws", Timeout: 60 * time.Second}
}

func (s *CredentialSource) CredentialProcess(profile string, granted map[string]bool) (json.RawMessage, error) {
	if !granted[profile] {
		return nil, &Error{http.StatusForbidden, fmt.Sprintf("profile %s is not granted to this sandbox", profile)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
	defer cancel()
	result, err := s.Run(ctx, []string{s.AWS, "configure", "export-credentials", "--profile", profile, "--format", "process"})
	if err != nil {
		return nil, &Error{http.StatusServiceUnavailable, fmt.Sprintf("%s: aws cli failed: %v", profile, err)}
	}
	if result.ExitCode != 0 {
		detail := lastLine(result.Stderr)
		if detail == "" {
			detail = "unknown error"
		}
		return nil, &Error{http.StatusServiceUnavailable, fmt.Sprintf("%s: %s; refresh the profile's login on the host", profile, detail)}
	}
	var credentials map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &credentials); err != nil || credentials == nil {
		return nil, &Error{http.StatusBadGateway, profile + ": aws cli returned invalid JSON"}
	}
	for _, key := range []string{"AccessKeyId", "SecretAccessKey"} {
		if value, _ := credentials[key].(string); value == "" {
			return nil, &Error{http.StatusBadGateway, profile + ": aws cli returned incomplete credentials"}
		}
	}
	for _, key := range []string{"SessionToken", "Expiration"} {
		if value, _ := credentials[key].(string); value == "" {
			return nil, &Error{http.StatusForbidden, profile + ": long-lived keys are not served to sandboxes; use an SSO or assumed-role profile, whose credentials expire"}
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(result.Stdout)); err != nil {
		return nil, &Error{http.StatusBadGateway, profile + ": aws cli returned invalid JSON"}
	}
	return compact.Bytes(), nil
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

type Grants struct{ Path string }

func (g Grants) Issue(owner string, profiles []string) (string, error) {
	if err := os.MkdirAll(g.Path, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(g.Path, 0o700); err != nil {
		return "", err
	}
	tokenFile := filepath.Join(g.Path, owner+".token")
	if _, err := os.Stat(tokenFile); errors.Is(err, os.ErrNotExist) {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		if err := writePrivate(tokenFile, hex.EncodeToString(random)); err != nil {
			return "", err
		}
	}
	return tokenFile, writePrivate(filepath.Join(g.Path, owner+".profiles"), strings.Join(profiles, "\n")+"\n")
}

func (g Grants) Revoke(owner string) error {
	for _, suffix := range []string{".token", ".profiles"} {
		if err := os.Remove(filepath.Join(g.Path, owner+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (g Grants) Lookup(supplied string) (string, map[string]bool, bool) {
	if supplied == "" {
		return "", nil, false
	}
	entries, err := os.ReadDir(g.Path)
	if err != nil {
		return "", nil, false
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".token") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(g.Path, name))
		if err != nil {
			continue
		}
		token := strings.TrimSpace(string(content))
		if token == "" || subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
			continue
		}
		owner := strings.TrimSuffix(name, ".token")
		profiles := map[string]bool{}
		if listing, err := os.ReadFile(filepath.Join(g.Path, owner+".profiles")); err == nil {
			for _, profile := range strings.Fields(string(listing)) {
				profiles[profile] = true
			}
		}
		return owner, profiles, true
	}
	return "", nil, false
}

func writePrivate(path, text string) error {
	temporary := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(text)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, os.Chmod(temporary, 0o600)); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func Handler(source Credentials, grants Grants, log func(string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.RequestURI
		send := func(status int, payload any, owner string) {
			body, _ := json.Marshal(payload)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(status)
			w.Write(body)
			log(fmt.Sprintf("broker: %s %s %s %d", owner, r.Method, path, status))
		}
		if r.Method != http.MethodGet {
			send(http.StatusNotImplemented, map[string]string{"error": "unsupported method"}, "-")
			return
		}
		supplied := r.Header.Get("Authorization")
		bearer, isBearer := strings.CutPrefix(supplied, "Bearer ")
		if !isBearer {
			bearer = ""
		}
		owner, profiles, ok := grants.Lookup(bearer)
		if !ok {
			send(http.StatusUnauthorized, map[string]string{"error": "missing or unknown broker token"}, "-")
			return
		}
		if !strings.HasPrefix(path, Prefix) {
			send(http.StatusNotFound, map[string]string{"error": "unknown path"}, owner)
			return
		}
		credentials, err := source.CredentialProcess(path[len(Prefix):], profiles)
		var brokerErr *Error
		switch {
		case errors.As(err, &brokerErr):
			send(brokerErr.Status, map[string]string{"error": brokerErr.Error()}, owner)
		case err != nil:
			send(http.StatusInternalServerError, map[string]string{"error": err.Error()}, owner)
		default:
			send(http.StatusOK, credentials, owner)
		}
	})
}

func Serve(grantsDir string, out io.Writer) error {
	listener, err := net.Listen("tcp", Address)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "broker: listening on %s; grants in %s\n", Address, grantsDir)
	var mu sync.Mutex
	log := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(out, line)
	}
	server := &http.Server{
		Handler:           Handler(NewCredentialSource(ExecRunner), Grants{grantsDir}, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.Serve(listener)
}
