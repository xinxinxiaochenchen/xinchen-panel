package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"controlplane/internal/agentidentity"
)

func WriteTokenOutput(path string, token agentidentity.EnrollmentToken) error {
	file, err := ReserveTokenOutput(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Write(token)
}

type TokenOutput struct{ file *os.File }

func ReserveTokenOutput(path string) (*TokenOutput, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("invalid Agent token output")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	return &TokenOutput{file: file}, nil
}

func (output *TokenOutput) Close() error { return output.file.Close() }

func (output *TokenOutput) Write(token agentidentity.EnrollmentToken) error {
	if token.Token == "" {
		return errors.New("empty Agent token")
	}
	encoded, err := json.Marshal(struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}{Token: token.Token, ExpiresAt: token.ExpiresAt})
	if err != nil {
		return err
	}
	if _, err := output.file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err := output.file.Sync(); err != nil {
		return err
	}
	return nil
}
