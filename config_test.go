package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigFlagsRejectsArguments(t *testing.T) {
	if err := parseConfigFlags(nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := parseConfigFlags([]string{"extra"}, io.Discard); err == nil {
		t.Fatal("config argument was accepted")
	}
}

func TestWriteConfigTableSortsPortsAndShortensHome(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "tester")
	outside := filepath.Join(string(filepath.Separator), "srv", "foobar")
	inside := filepath.Join(home, "a", "b", "c", "d")
	registry := portsFile{
		Version: portsFileVersion,
		Roots: map[string]portEntry{
			inside:  {Port: 50432},
			outside: {Port: 7777},
		},
	}
	var output bytes.Buffer
	if err := writeConfigTable(&output, registry, home, map[int][]int{
		50432: {1234},
	}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("table = %q", output.String())
	}
	want := [][]string{
		{"Pid", "Port", "Root"},
		{"-", "7777", outside},
		{"1234", "50432", filepath.Join("~", "a", "b", "c", "d")},
	}
	for index, line := range lines {
		fields := strings.Fields(line)
		if strings.Join(fields, "|") != strings.Join(want[index], "|") {
			t.Errorf("line %d = %q, want fields %q", index, line, want[index])
		}
	}
}

func TestParseLsofListeners(t *testing.T) {
	listeners := parseLsofListeners([]byte(strings.Join([]string{
		"p1234",
		"n127.0.0.1:7777",
		"n[::1]:7777",
		"p5678",
		"n*:50432",
		"nmalformed",
	}, "\n")))
	if got := formatPIDs(listeners[7777]); got != "1234" {
		t.Errorf("port 7777 PIDs = %q", got)
	}
	if got := formatPIDs(listeners[50432]); got != "5678" {
		t.Errorf("port 50432 PIDs = %q", got)
	}
	if got := formatPIDs(nil); got != "-" {
		t.Errorf("inactive PID = %q", got)
	}
}

func TestRunConfigWithNoRegistryPrintsHeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := run([]string{"config"}, &stdout, &stderr); err != nil {
		t.Fatalf("run config: %v; stderr=%s", err, stderr.String())
	}
	if fields := strings.Fields(stdout.String()); strings.Join(fields, "|") != "Pid|Port|Root" {
		t.Errorf("output = %q", stdout.String())
	}
}
