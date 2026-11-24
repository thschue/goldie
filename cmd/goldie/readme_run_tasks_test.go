package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// #683: every `./run <task>` the README names must be a task `./run help`
// lists, so a first-run step never sends a new user to a task that is gone.
func TestREADMERunTasksAreListedByRunHelp(t *testing.T) {
	cmd := exec.Command("sh", "./run", "help")
	cmd.Dir = "../.."
	help, err := cmd.Output()
	if err != nil {
		t.Fatalf("./run help: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+\./run ([a-z][a-z0-9-]*)`).FindAllStringSubmatch(string(help), -1) {
		listed[m[1]] = true
	}
	if len(listed) == 0 {
		t.Fatal("./run help lists no tasks")
	}
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	for _, m := range regexp.MustCompile(`\./run ([a-z][a-z0-9-]*)`).FindAllStringSubmatch(readme, -1) {
		if !listed[m[1]] {
			t.Errorf("README names ./run %s, which ./run help does not list", m[1])
		}
	}
	if !strings.Contains(readme, "./run ui") {
		t.Error("README no longer names ./run ui in its first-run steps")
	}
}
