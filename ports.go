package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const portsFileVersion = 1

type portsFile struct {
	Version int                  `json:"version"`
	Roots   map[string]portEntry `json:"roots"`
}

type portEntry struct {
	Port int `json:"port"`
}

func portsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".mdfmt", "ports.json"), nil
}

func readPortsFile(filename string) (portsFile, error) {
	registry := portsFile{
		Version: portsFileVersion,
		Roots:   make(map[string]portEntry),
	}
	content, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return portsFile{}, fmt.Errorf("read port registry %s: %w", filename, err)
	}
	if err := json.Unmarshal(content, &registry); err != nil {
		return portsFile{}, fmt.Errorf("parse port registry %s: %w", filename, err)
	}
	if registry.Version != portsFileVersion {
		return portsFile{}, fmt.Errorf(
			"port registry %s has unsupported version %d",
			filename,
			registry.Version,
		)
	}
	if registry.Roots == nil {
		registry.Roots = make(map[string]portEntry)
	}
	for root, entry := range registry.Roots {
		if !filepath.IsAbs(root) {
			return portsFile{}, fmt.Errorf("port registry %s contains a non-absolute root %q", filename, root)
		}
		if entry.Port < 1 || entry.Port > 65535 {
			return portsFile{}, fmt.Errorf(
				"port registry %s contains invalid port %d for %s",
				filename,
				entry.Port,
				root,
			)
		}
	}
	return registry, nil
}

func writePortsFile(filename string, registry portsFile) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create port registry directory %s: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, ".ports.json-*")
	if err != nil {
		return fmt.Errorf("create temporary port registry: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set port registry permissions: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(registry); err != nil {
		temporary.Close()
		return fmt.Errorf("encode port registry: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync port registry: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close port registry: %w", err)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("replace port registry %s: %w", filename, err)
	}
	return nil
}

func listenForServe(cfg serveConfig, root string) (net.Listener, error) {
	filename, err := portsFilePath()
	if err != nil {
		return nil, err
	}
	return listenWithPortRegistry(cfg, root, filename)
}

func listenWithPortRegistry(cfg serveConfig, root, filename string) (net.Listener, error) {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create port registry directory %s: %w", directory, err)
	}
	lock, err := os.OpenFile(filename+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open port registry lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock port registry: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	registry, err := readPortsFile(filename)
	if err != nil {
		return nil, err
	}

	port := cfg.port
	entry, remembered := registry.Roots[root]
	if !cfg.portSet && remembered {
		port = entry.Port
	}
	listener, selectedPort, err := listenOnRegisteredPort(cfg.bind, port, root, registry)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			owner := rootForPort(registry, port)
			if owner != "" && owner != root {
				return nil, fmt.Errorf(
					"port %d remembered for %s is already in use; choose another port",
					port,
					owner,
				)
			}
			if remembered && port == entry.Port {
				return nil, fmt.Errorf(
					"port %d remembered for %s is already in use; choose a different port with: mdfmt serve --port PORT %q",
					port,
					root,
					root,
				)
			}
			return nil, fmt.Errorf(
				"port %d is already in use",
				port,
			)
		}
		return nil, err
	}
	for registeredRoot, registeredEntry := range registry.Roots {
		if registeredRoot != root && registeredEntry.Port == selectedPort {
			delete(registry.Roots, registeredRoot)
		}
	}
	registry.Roots[root] = portEntry{Port: selectedPort}
	if err := writePortsFile(filename, registry); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

func listenOnRegisteredPort(bind string, port int, root string, registry portsFile) (net.Listener, int, error) {
	for {
		listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			return nil, 0, err
		}
		selectedPort, err := listenerPort(listener)
		if err != nil {
			listener.Close()
			return nil, 0, err
		}
		if port != 0 || rootForOtherPort(registry, selectedPort, root) == "" {
			return listener, selectedPort, nil
		}
		if err := listener.Close(); err != nil {
			return nil, 0, fmt.Errorf("release selected port %d: %w", selectedPort, err)
		}
	}
}

func listenerPort(listener net.Listener) (int, error) {
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return 0, fmt.Errorf("read selected port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0, fmt.Errorf("read selected port: %w", err)
	}
	return port, nil
}

func rootForPort(registry portsFile, port int) string {
	for root, entry := range registry.Roots {
		if entry.Port == port {
			return root
		}
	}
	return ""
}

func rootForOtherPort(registry portsFile, port int, excludedRoot string) string {
	for root, entry := range registry.Roots {
		if root != excludedRoot && entry.Port == port {
			return root
		}
	}
	return ""
}
