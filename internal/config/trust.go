package config

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// Approval is something a repository asked for that widens its sandbox: one
// of its kits, or an installed book it turned on.
type Approval struct {
	Label       string
	Fingerprint string
	Details     []string
}

func trustFile(home string) string { return filepath.Join(home, "trusted-kits") }

// RepoApprovals lists what the repository's config asked for that needs the user's approval.
func RepoApprovals(composition *Composition) ([]Approval, error) {
	var approvals []Approval
	for _, kit := range composition.RepoKits {
		fingerprint, err := KitFingerprint(kit)
		if err != nil {
			return nil, err
		}
		approval := Approval{Label: "kit " + kit.Source, Fingerprint: fingerprint}
		if kit.IsLocal() {
			approval.Details = SummarizeKit(kit.Source)
		} else {
			approval.Details = []string{"published kit; see what it does with `sbx kit inspect " + kit.Source + "`"}
		}
		approvals = append(approvals, approval)
	}
	for _, book := range composition.RepoBooks {
		fingerprint, err := hashTree("book\x00"+book.Name, book.Path, true)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, Approval{Label: "book " + book.Name + " (" + book.Path + ")", Fingerprint: fingerprint, Details: summarizeBook(book)})
	}
	for _, approval := range approvals {
		if HasControl(approval.Label) {
			return nil, errorf("%s contains control characters; refusing to ask for its approval", Printable(approval.Label))
		}
	}
	return approvals, nil
}

// KitFingerprint identifies a local kit by its files and a published kit by its reference, each with its arguments.
func KitFingerprint(kit Kit) (string, error) {
	args, err := json.Marshal(kit.Args)
	if err != nil {
		return "", err
	}
	if !kit.IsLocal() {
		hash := sha256.Sum256([]byte("ref\x00" + kit.Source + "\x00" + string(args)))
		return hex.EncodeToString(hash[:]), nil
	}
	return hashTree("dir\x00"+string(args), kit.Source, false)
}

func hashTree(prefix, root string, skipGit bool) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00", prefix)
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipGit && entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", errorf("cannot read %s: %v", root, err)
	}
	sort.Strings(files)
	for _, path := range files {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		relative, _ := filepath.Rel(root, path)
		fmt.Fprintf(hash, "%s\x00%o\x00", relative, info.Mode())
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(hash, "%s\x00", target)
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%d\x00", len(content))
		hash.Write(content)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type trustRecord struct {
	Fingerprint string `json:"fingerprint"`
	Label       string `json:"label"`
}

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func readTrust(home string) (records []trustRecord, legacy bool, err error) {
	file, err := os.Open(trustFile(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "{") {
			legacy = true
			continue
		}
		var record trustRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil || !fingerprintPattern.MatchString(record.Fingerprint) {
			return nil, false, errorf("%s:%d: not a trust record; fix or remove the line", trustFile(home), number)
		}
		records = append(records, record)
	}
	return records, legacy, scanner.Err()
}

func syncDirectory(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func writeTrust(home string, records []trustRecord) error {
	var content strings.Builder
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		content.Write(line)
		content.WriteByte('\n')
	}
	temporary, err := os.CreateTemp(home, ".trusted-kits-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.WriteString(content.String())
	if err := errors.Join(writeErr, temporary.Chmod(0o600), temporary.Sync(), temporary.Close()); err != nil {
		return err
	}
	if err := syncDirectory(home); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), trustFile(home)); err != nil {
		return err
	}
	return syncDirectory(home)
}

func lockTrust(home string) (func(), error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(trustFile(home)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}, nil
}

func ClearLegacyTrust(home string) (bool, error) {
	if _, err := os.Stat(trustFile(home)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	unlock, err := lockTrust(home)
	if err != nil {
		return false, err
	}
	defer unlock()
	records, legacy, err := readTrust(home)
	if err != nil || !legacy {
		return false, err
	}
	return true, writeTrust(home, records)
}

func trusted(home string) (map[string]bool, error) {
	records, _, err := readTrust(home)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, record := range records {
		known[record.Fingerprint] = true
	}
	return known, nil
}

// Unapproved returns the approvals the user has not given yet.
func Unapproved(home string, approvals []Approval) ([]Approval, error) {
	known, err := trusted(home)
	if err != nil {
		return nil, err
	}
	var missing []Approval
	for _, approval := range approvals {
		if !known[approval.Fingerprint] {
			missing = append(missing, approval)
		}
	}
	return missing, nil
}

func Approve(home string, approvals []Approval) error {
	for _, approval := range approvals {
		if !fingerprintPattern.MatchString(approval.Fingerprint) || HasControl(approval.Label) {
			return errorf("cannot record approval of %s", Printable(approval.Label))
		}
	}
	unlock, err := lockTrust(home)
	if err != nil {
		return err
	}
	defer unlock()
	records, _, err := readTrust(home)
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		records = append(records, trustRecord{Fingerprint: approval.Fingerprint, Label: approval.Label})
	}
	return writeTrust(home, records)
}

func summarizeBook(book Book) []string {
	var details []string
	agentLine := func(entry any, where string) {
		if name, ok := entry.(string); ok && looksLikeName(name) {
			if spec, known := agents[name]; known {
				details = append(details, fmt.Sprintf("%sagent %s pinned to %s", where, name, spec.kit))
				return
			}
		}
		kit, err := parseKit(entry, book.Path, "book "+book.Name)
		if err != nil {
			details = append(details, fmt.Sprintf("%sagent %v: unrecognised; read the book before approving", where, entry))
			return
		}
		details = append(details, fmt.Sprintf("%sagent kit %s", where, kit.Source))
	}
	for _, entry := range book.agents {
		agentLine(entry, "")
	}
	if len(book.aws.profiles) > 0 {
		details = append(details, "AWS profiles: "+strings.Join(book.aws.profiles, " "))
	}
	for _, service := range sortedKeys(book.secrets) {
		if table, ok := book.secrets[service].(map[string]any); ok {
			if command, ok := table["command"].(string); ok {
				details = append(details, fmt.Sprintf("host command for secret %s (runs on your machine; files it reads outside the book are not covered by this approval): %s", service, command))
				continue
			}
		}
		details = append(details, "secret "+service)
	}
	for _, service := range sortedKeys(book.bindings) {
		domains, err := bindingDomains(book.bindings[service])
		if err != nil {
			details = append(details, fmt.Sprintf("credential %s: unrecognised binding; read the book before approving", service))
			continue
		}
		details = append(details, fmt.Sprintf("credential %s sent to %s", service, strings.Join(domains, ", ")))
	}
	for _, entry := range book.kits {
		kit, err := parseKit(entry, book.Path, "book "+book.Name)
		if err != nil {
			continue
		}
		if kit.IsLocal() {
			for _, line := range SummarizeKit(kit.Source) {
				details = append(details, filepath.Base(kit.Source)+": "+line)
			}
		} else {
			details = append(details, "published kit "+kit.Source)
		}
	}
	if len(book.repos) > 0 {
		details = append(details, fmt.Sprintf("%d per-repository rule(s)", len(book.repos)))
	}
	for _, r := range book.repos {
		for _, entry := range r.agents {
			agentLine(entry, "repos matching "+strings.Join(r.patterns, ", ")+": ")
		}
	}
	for i, detail := range details {
		details[i] = Printable(detail)
	}
	return details
}
