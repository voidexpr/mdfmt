package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

const (
	pathTokenAuto = "auto"
	pathTokenNone = "none"
	pathTokenSize = 16
)

var validPathToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validatePathTokenOption(value string) error {
	if value == pathTokenAuto || value == pathTokenNone {
		return nil
	}
	if value == "" {
		return errors.New("--path-token must be auto, none, or a non-empty token")
	}
	if len(value) > 128 || !validPathToken.MatchString(value) {
		return errors.New("--path-token must contain 1-128 letters, digits, underscores, or hyphens")
	}
	return nil
}

func generatePathToken() (string, error) {
	value := make([]byte, pathTokenSize)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate path token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func tokenPointer(value string) *string {
	return &value
}

func servedURL(pathToken string, components []string, directory bool) string {
	if pathToken == "" {
		return escapedURL(components, directory)
	}
	prefixed := make([]string, 0, len(components)+1)
	prefixed = append(prefixed, pathToken)
	prefixed = append(prefixed, components...)
	return escapedURL(prefixed, directory)
}
