package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type openConfig struct {
	paths     []string
	port      int
	portSet   bool
	root      string
	bind      string
	pathToken string
	chrome    bool
	printOnly bool
}

const chromeExecutable = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

func parseOpenFlags(args []string, output io.Writer) (openConfig, error) {
	cfg := openConfig{bind: defaultBind, pathToken: pathTokenAuto}
	flags := newFlagSet("mdfmt open", output,
		"Usage: mdfmt open PATH... [--port PORT] [--root PATH] [--bind ADDRESS] [--path-token VALUE] [--chrome] [--print-only]",
		"Print URLs for files or directories served by mdfmt and open them in a browser.",
		"Without --root, each path uses its longest matching root from ~/.mdfmt/ports.json.",
		"Flags may appear before, after, or between paths.")
	flags.IntVar(&cfg.port, "port", 0, "override the remembered TCP port")
	flags.StringVar(&cfg.root, "root", "", "use this root for every path")
	flags.StringVar(&cfg.bind, "bind", defaultBind, "address to use in generated URLs")
	flags.StringVar(&cfg.pathToken, "path-token", pathTokenAuto, "URL path token: auto, none, or a custom value")
	flags.BoolVar(&cfg.chrome, "chrome", false, "open with Google Chrome on macOS")
	flags.BoolVar(&cfg.printOnly, "print-only", false, "print URLs without opening a browser")
	if err := flags.Parse(args); err != nil {
		return openConfig{}, err
	}
	cfg.portSet = flags.Changed("port")
	if flags.NArg() == 0 {
		flags.Usage()
		return openConfig{}, errors.New("expected at least one PATH")
	}
	cfg.paths = flags.Args()
	if cfg.portSet && (cfg.port < 1 || cfg.port > 65535) {
		return openConfig{}, errors.New("--port must be between 1 and 65535")
	}
	if net.ParseIP(cfg.bind) == nil {
		return openConfig{}, fmt.Errorf("--bind must be an IP address, got %q", cfg.bind)
	}
	if err := validatePathTokenOption(cfg.pathToken); err != nil {
		return openConfig{}, err
	}
	return cfg, nil
}

func runOpen(cfg openConfig, stdout io.Writer) error {
	registry := portsFile{
		Version: portsFileVersion,
		Roots:   make(map[string]portEntry),
	}
	if cfg.root == "" || !cfg.portSet || cfg.pathToken == pathTokenAuto {
		filename, err := portsFilePath()
		if err != nil {
			return err
		}
		registry, err = readPortsFile(filename)
		if err != nil {
			return err
		}
	}
	urls, err := openURLs(cfg, registry)
	if err != nil {
		return err
	}
	for _, targetURL := range urls {
		fmt.Fprintln(stdout, targetURL)
	}
	if cfg.printOnly {
		return nil
	}
	if cfg.chrome || defaultBrowserIsChrome(urls[0]) {
		return launchChrome(urls)
	}
	return launchBrowser(urls)
}

func openURLs(cfg openConfig, registry portsFile) ([]string, error) {
	var forcedRoot string
	if cfg.root != "" {
		resolved, info, err := resolveExisting(cfg.root)
		if err != nil {
			return nil, fmt.Errorf("resolve --root: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("--root is not a directory: %s", resolved)
		}
		forcedRoot = resolved
	}

	urls := make([]string, 0, len(cfg.paths))
	for _, suppliedPath := range cfg.paths {
		resolved, info, err := resolveExisting(suppliedPath)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", suppliedPath, err)
		}
		if !info.IsDir() && (!info.Mode().IsRegular() || !isMarkdownName(info.Name())) {
			return nil, fmt.Errorf("path is not a directory or Markdown file: %s", resolved)
		}

		root := forcedRoot
		if root == "" {
			root = longestMatchingRoot(resolved, registry)
			if root == "" {
				return nil, fmt.Errorf("no configured mdfmt root contains %s", resolved)
			}
		} else if !withinRoot(root, resolved) {
			return nil, fmt.Errorf("path %s is outside --root %s", resolved, root)
		}
		if hasHiddenComponent(root, resolved) {
			return nil, fmt.Errorf("path contains a hidden component beneath root %s: %s", root, resolved)
		}

		port := cfg.port
		entry, registered := registry.Roots[root]
		if !cfg.portSet {
			if !registered {
				return nil, fmt.Errorf("no remembered port for root %s", root)
			}
			port = entry.Port
		}
		pathToken := ""
		switch cfg.pathToken {
		case pathTokenAuto:
			if !registered || entry.PathToken == nil {
				return nil, fmt.Errorf("no remembered path token for root %s; specify --path-token", root)
			}
			pathToken = *entry.PathToken
		case pathTokenNone:
			pathToken = ""
		default:
			pathToken = cfg.pathToken
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			return nil, fmt.Errorf("make %s relative to %s: %w", resolved, root, err)
		}
		var components []string
		if relative != "." {
			components = strings.Split(relative, string(filepath.Separator))
		}
		route := servedURL(pathToken, components, info.IsDir())
		host := net.JoinHostPort(cfg.bind, strconv.Itoa(port))
		urls = append(urls, "http://"+host+route)
	}
	return urls, nil
}

func longestMatchingRoot(path string, registry portsFile) string {
	var match string
	for root := range registry.Roots {
		if withinRoot(root, path) && len(root) > len(match) {
			match = root
		}
	}
	return match
}

func launchBrowser(urls []string) error {
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		command = "open"
	}
	executable, err := exec.LookPath(command)
	if errors.Is(err, exec.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find browser opener %q: %w", command, err)
	}
	if command == "xdg-open" {
		for _, targetURL := range urls {
			if err := exec.Command(executable, targetURL).Run(); err != nil {
				return fmt.Errorf("open %s: %w", targetURL, err)
			}
		}
		return nil
	}
	if err := exec.Command(executable, urls...).Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func launchChrome(urls []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("--chrome is only supported on macOS")
	}
	executable, err := exec.LookPath(chromeExecutable)
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("find Google Chrome at %s: executable not found", chromeExecutable)
	}
	if err != nil {
		return fmt.Errorf("find Google Chrome: %w", err)
	}
	for _, targetURL := range urls {
		if err := exec.Command(executable, chromeArguments(targetURL)...).Run(); err != nil {
			return fmt.Errorf("open %s in Google Chrome: %w", targetURL, err)
		}
	}
	return nil
}

func chromeArguments(targetURL string) []string {
	return []string{"--focus=" + targetURL + "/*", targetURL}
}
