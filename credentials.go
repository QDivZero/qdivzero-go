package qdivzero

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// credentialsFile is the JSON format of the optional ~/.qdivzero/credentials
// file. All fields are optional.
type credentialsFile struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// loadCredentialsFile reads ~/.qdivzero/credentials, if present. A missing
// file is not an error; an unreadable or malformed file is.
func loadCredentialsFile() (credentialsFile, error) {
	var cf credentialsFile
	home, err := os.UserHomeDir()
	if err != nil {
		return cf, fmt.Errorf("qdivzero: home dir: %w", err)
	}
	path := filepath.Join(home, ".qdivzero", "credentials")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cf, nil
		}
		return cf, fmt.Errorf("qdivzero: read credentials: %w", err)
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		return cf, fmt.Errorf("qdivzero: parse credentials: %w", err)
	}
	return cf, nil
}
