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
	"sort"
	"strings"
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

func trusted(home string) (map[string]bool, error) {
	known := map[string]bool{}
	file, err := os.Open(trustFile(home))
	if errors.Is(err, os.ErrNotExist) {
		return known, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) > 0 {
			known[fields[0]] = true
		}
	}
	return known, scanner.Err()
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
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(trustFile(home), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, approval := range approvals {
		if _, err := fmt.Fprintf(file, "%s %s\n", approval.Fingerprint, approval.Label); err != nil {
			return err
		}
	}
	return nil
}

func summarizeBook(book Book) []string {
	var details []string
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
		details = append(details, fmt.Sprintf("credential %s sent to %s", service, strings.Join(domainsOf(book.bindings[service]), ", ")))
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
	return details
}

func domainsOf(binding any) []string {
	var found []string
	kinds, _ := binding.(map[string]any)
	for _, kind := range kinds {
		table, _ := kind.(map[string]any)
		list, _ := asList(table["domains"])
		for _, domain := range list {
			found = append(found, fmt.Sprint(domain))
		}
	}
	return found
}
