package main

import (
	"errors"
	"strings"
	"testing"

	"grove/internal/config"
)

func TestNoUserConfigMessageNamesFileAndProblem(t *testing.T) {
	err := &config.UserConfigError{
		Path:    "/home/u/.config/grove/config.json",
		Problem: "not found",
	}

	msg := noUserConfigMessage("/home/u/Code/slakkr", err)

	for _, want := range []string{
		"/home/u/Code/slakkr is not a grove project",
		"/home/u/.config/grove/config.json",
		"the file was not found",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q, got:\n%s", want, msg)
		}
	}
}

func TestNoUserConfigMessageIncludesUnderlyingCause(t *testing.T) {
	err := &config.UserConfigError{
		Path:    "/home/u/.config/grove/config.jsonc",
		Problem: "not valid JSON",
		Err:     errors.New("invalid character 'o'"),
	}

	msg := noUserConfigMessage("/tmp/x", err)

	if !strings.Contains(msg, "the file was not valid JSON") {
		t.Errorf("message missing the problem, got:\n%s", msg)
	}
	if !strings.Contains(msg, "invalid character 'o'") {
		t.Errorf("message missing the underlying cause, got:\n%s", msg)
	}
}

func TestNoUserConfigMessageFallsBackToPlainError(t *testing.T) {
	msg := noUserConfigMessage("/tmp/x", errors.New("boom"))

	if !strings.Contains(msg, "boom") || !strings.Contains(msg, "/tmp/x") {
		t.Errorf("unexpected message for a non-UserConfigError:\n%s", msg)
	}
}
