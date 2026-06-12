package email

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Account holds the IMAP settings for one mailbox. The address doubles as the
// IMAP login username, which is what every common provider expects.
type Account struct {
	Address     string `json:"address"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Password    string `json:"password"`
	Folder      string `json:"folder"`      // defaults to INBOX
	InitialDays int    `json:"initialDays"` // first-sync window; defaults to 30
}

// Addr returns the host:port dial address.
func (a Account) Addr() string {
	port := a.Port
	if port == 0 {
		port = 993
	}
	return fmt.Sprintf("%s:%d", a.Host, port)
}

// FolderOrDefault returns the configured folder, defaulting to INBOX.
func (a Account) FolderOrDefault() string {
	if a.Folder == "" {
		return "INBOX"
	}
	return a.Folder
}

// knownHosts maps well-known email domains to their IMAP servers so that
// `ib email add` only needs --host for self-hosted domains.
var knownHosts = map[string]string{
	"gmail.com":      "imap.gmail.com",
	"googlemail.com": "imap.gmail.com",
	"yahoo.com":      "imap.mail.yahoo.com",
	"yahoo.com.sg":   "imap.mail.yahoo.com",
	"ymail.com":      "imap.mail.yahoo.com",
	"outlook.com":    "outlook.office365.com",
	"hotmail.com":    "outlook.office365.com",
	"live.com":       "outlook.office365.com",
	"icloud.com":     "imap.mail.me.com",
	"me.com":         "imap.mail.me.com",
}

// DefaultHost returns the IMAP server for a well-known email domain, or ""
// when the domain is not recognized (e.g. a self-hosted domain).
func DefaultHost(address string) string {
	_, domain, ok := strings.Cut(address, "@")
	if !ok {
		return ""
	}
	return knownHosts[strings.ToLower(domain)]
}

// AccountsPath returns the email accounts file inside a home directory.
func AccountsPath(home string) string {
	return filepath.Join(home, "email_accounts.json")
}

type accountsFile struct {
	Accounts []Account `json:"accounts"`
}

// LoadAccounts reads the accounts file; a missing file means no accounts.
func LoadAccounts(home string) ([]Account, error) {
	data, err := os.ReadFile(AccountsPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read email accounts: %w", err)
	}
	var f accountsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", AccountsPath(home), err)
	}
	return f.Accounts, nil
}

// SaveAccounts writes the accounts file (0600 — it holds passwords).
func SaveAccounts(home string, accounts []Account) error {
	data, err := json.MarshalIndent(accountsFile{Accounts: accounts}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(AccountsPath(home), append(data, '\n'), 0o600)
}
