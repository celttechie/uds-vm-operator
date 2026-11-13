package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewVersionCmd(t *testing.T) {
	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error executing version command: %v", err)
	}
}

func TestNewPrepCmd_MissingArgs(t *testing.T) {
	cmd := newPrepCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error executing prep without image arg, got nil")
	}
}

func TestNewPowerCmd_MissingArgs(t *testing.T) {
	cmd := newPowerCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error executing power without args, got nil")
	}
}

func TestNewInventoryCmd_Help(t *testing.T) {
	cmd := newInventoryCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error displaying inventory help: %v", err)
	}
	if !strings.Contains(buf.String(), "inventory") {
		t.Errorf("expected help output to mention 'inventory', got: %s", buf.String())
	}
}
