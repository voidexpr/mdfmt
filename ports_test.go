package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestManagedPortIsRecordedAndReused(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), ".mdfmt", "ports.json")

	cfg := serveConfig{bind: defaultBind}
	first, err := listenWithPortRegistry(cfg, root, registryPath)
	if err != nil {
		t.Fatal(err)
	}
	firstPort := first.Addr().(*net.TCPAddr).Port
	if firstPort == 0 {
		t.Fatal("operating system did not select a port")
	}

	registry, err := readPortsFile(registryPath)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	if got := registry.Roots[root].Port; got != firstPort {
		first.Close()
		t.Fatalf("remembered port = %d, want %d", got, firstPort)
	}
	info, err := os.Stat(registryPath)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		first.Close()
		t.Errorf("registry permissions = %o, want 600", got)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := listenWithPortRegistry(cfg, root, registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := second.Addr().(*net.TCPAddr).Port; got != firstPort {
		t.Errorf("reused port = %d, want %d", got, firstPort)
	}
}

func TestManagedPortCollisionReportsTemporaryOverride(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), ".mdfmt", "ports.json")
	occupied, err := net.Listen("tcp", net.JoinHostPort(defaultBind, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	if err := writePortsFile(registryPath, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{root: {Port: port}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = listenWithPortRegistry(serveConfig{bind: defaultBind}, root, registryPath)
	if err == nil {
		t.Fatal("occupied remembered port was accepted")
	}
	for _, want := range []string{
		"port " + strconv.Itoa(port) + " remembered for " + root + " is already in use",
		"mdfmt serve --port PORT",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	registry, readErr := readPortsFile(registryPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := registry.Roots[root].Port; got != port {
		t.Errorf("collision changed remembered port to %d, want %d", got, port)
	}
}

func TestExplicitPortIsRecordedAndReplacesRootPort(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), ".mdfmt", "ports.json")
	if err := writePortsFile(registryPath, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{root: {Port: 61001}},
	}); err != nil {
		t.Fatal(err)
	}
	available, err := net.Listen("tcp", net.JoinHostPort(defaultBind, "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := available.Addr().(*net.TCPAddr).Port
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}

	listener, err := listenWithPortRegistry(serveConfig{
		bind:    defaultBind,
		port:    port,
		portSet: true,
	}, root, registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	registry, err := readPortsFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Roots[root].Port; got != port {
		t.Errorf("remembered port = %d, want explicit port %d", got, port)
	}
}

func TestExplicitPortTakesOverStaleRootEntry(t *testing.T) {
	root := t.TempDir()
	staleRoot := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), ".mdfmt", "ports.json")
	available, err := net.Listen("tcp", net.JoinHostPort(defaultBind, "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := available.Addr().(*net.TCPAddr).Port
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writePortsFile(registryPath, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{staleRoot: {Port: port}},
	}); err != nil {
		t.Fatal(err)
	}

	listener, err := listenWithPortRegistry(serveConfig{
		bind:    defaultBind,
		port:    port,
		portSet: true,
	}, root, registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	registry, err := readPortsFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Roots[staleRoot]; ok {
		t.Errorf("stale root %s still owns port %d", staleRoot, port)
	}
	if got := registry.Roots[root].Port; got != port {
		t.Errorf("new root port = %d, want %d", got, port)
	}
}

func TestExplicitOccupiedPortPreservesOtherRootEntry(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), ".mdfmt", "ports.json")
	occupied, err := net.Listen("tcp", net.JoinHostPort(defaultBind, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	if err := writePortsFile(registryPath, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{otherRoot: {Port: port}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = listenWithPortRegistry(serveConfig{
		bind:    defaultBind,
		port:    port,
		portSet: true,
	}, root, registryPath)
	if err == nil {
		t.Fatal("occupied explicit port was accepted")
	}
	if !strings.Contains(err.Error(), "remembered for "+otherRoot+" is already in use") {
		t.Errorf("error = %q", err)
	}
	registry, readErr := readPortsFile(registryPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := registry.Roots[otherRoot].Port; got != port {
		t.Errorf("other root port = %d, want %d", got, port)
	}
	if _, ok := registry.Roots[root]; ok {
		t.Errorf("failed bind added root %s", root)
	}
}

func TestExplicitZeroPortIsRecorded(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), ".mdfmt", "ports.json")
	listener, err := listenWithPortRegistry(serveConfig{
		bind:    defaultBind,
		portSet: true,
	}, root, registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	registry, err := readPortsFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Roots[root].Port; got != port {
		t.Errorf("remembered port = %d, want selected port %d", got, port)
	}
}

func TestReadPortsFileRejectsInvalidData(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "ports.json")
	if err := os.WriteFile(filename, []byte(`{"version":1,"roots":{"/docs":{"port":0}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPortsFile(filename); err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("invalid registry error = %v", err)
	}

	if err := os.WriteFile(filename, []byte(`{"version":2,"roots":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPortsFile(filename); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("version error = %v", err)
	}
}
