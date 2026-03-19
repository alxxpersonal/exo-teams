package auth

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	fossteamsDir = ".config/fossteams"
)

// ImportFromFossteams copies tokens from the fossteams config directory.
// fossteams stores tokens as: skypeToken, chatSvcAggToken, teamsToken
func ImportFromFossteams() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	srcDir := filepath.Join(home, fossteamsDir)
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("fossteams directory not found at %s - make sure fossteams is installed and you have logged in", srcDir)
	}

	// fossteams stores tokens in individual files
	tokenMap := map[string]string{
		"token-skype.jwt":       skypeTokenFile,
		"token-chatsvcagg.jwt":  chatsvcTokenFile,
		"token-teams.jwt":       teamsTokenFile,
	}

	tokens := &Tokens{}
	for srcName := range tokenMap {
		srcPath := filepath.Join(srcDir, srcName)
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading %s from fossteams: %w", srcName, err)
		}
		val := string(data)
		if len(val) == 0 {
			return fmt.Errorf("token %s is empty in fossteams", srcName)
		}

		switch srcName {
		case "token-skype.jwt":
			tokens.Skype = val
		case "token-chatsvcagg.jwt":
			tokens.ChatSvcAgg = val
		case "token-teams.jwt":
			tokens.Teams = val
		}
	}

	if err := Save(tokens); err != nil {
		return fmt.Errorf("saving imported tokens: %w", err)
	}

	return nil
}

// CheckTokens verifies that tokens exist and reports their expiry status.
func CheckTokens() error {
	tokens, err := Load()
	if err != nil {
		return fmt.Errorf("loading tokens: %w", err)
	}

	type tokenInfo struct {
		name  string
		value string
	}

	checks := []tokenInfo{
		{"skype", tokens.Skype},
		{"chatsvcagg", tokens.ChatSvcAgg},
		{"teams", tokens.Teams},
		{"graph", tokens.Graph},
		{"assignments", tokens.Assignments},
	}

	for _, t := range checks {
		expired, err := IsExpired(t.value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: cannot check expiry (%v)\n", t.name, err)
			continue
		}
		expiry, _ := GetTokenExpiry(t.value)
		if expired {
			fmt.Fprintf(os.Stderr, "  %s: EXPIRED (was %s)\n", t.name, expiry.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Fprintf(os.Stderr, "  %s: valid until %s\n", t.name, expiry.Format("2006-01-02 15:04:05"))
		}
	}

	return nil
}
