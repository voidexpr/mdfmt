package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/pflag"
)

const defaultBind = "127.0.0.1"

type serveConfig struct {
	bind        string
	port        int
	portSet     bool
	root        string
	pathToken   string
	editCommand string
	editArgs    []string
	editSublime bool
	editVSCode  bool
}

type saveConfig struct {
	source string
	output string
	hash   bool
	quiet  bool
}

type buildConfig struct {
	source           string
	mounts           []string
	output           string
	pathToken        string
	strict           bool
	strictNoDirIndex bool
	quiet            bool
}

func newFlagSet(name string, output io.Writer, usage string, description ...string) *pflag.FlagSet {
	flags := pflag.NewFlagSet(name, pflag.ContinueOnError)
	flags.SetOutput(output)
	flags.SetInterspersed(true)
	flags.Usage = func() {
		fmt.Fprintln(output, usage)
		fmt.Fprintln(output)
		for _, line := range description {
			fmt.Fprintln(output, line)
		}
		flags.PrintDefaults()
	}
	return flags
}

func parseServeFlags(args []string, output io.Writer) (serveConfig, error) {
	cfg := serveConfig{bind: defaultBind, pathToken: pathTokenAuto}
	flags := newFlagSet("mdfmt serve", output,
		"Usage: mdfmt serve [--bind IP] [--port NUM] [--path-token VALUE] [PATH]",
		"Serve a directory of Markdown files as a read-only website.",
		"Editor flags optionally add controls that launch a local editor.",
		"Flags may appear before or after PATH.")
	flags.StringVar(&cfg.bind, "bind", defaultBind, "IP address to bind")
	flags.IntVar(&cfg.port, "port", 0, "TCP port (successful starts remember the actual port)")
	flags.StringVar(&cfg.pathToken, "path-token", pathTokenAuto, "URL path token: auto, none, or a custom value")
	flags.StringVar(&cfg.editCommand, "edit-command", "", "editor command to launch for Markdown files")
	flags.StringArrayVar(&cfg.editArgs, "edit-arg", nil, "editor argument before the file path (repeatable)")
	flags.BoolVar(&cfg.editSublime, "edit-sublime", false, "enable editing with the Sublime Text CLI")
	flags.BoolVar(&cfg.editVSCode, "edit-vscode", false, "enable editing with the Visual Studio Code CLI")
	if err := flags.Parse(args); err != nil {
		return serveConfig{}, err
	}
	cfg.portSet = flags.Changed("port")
	if flags.NArg() > 1 {
		flags.Usage()
		return serveConfig{}, fmt.Errorf("expected at most one PATH")
	}
	if flags.NArg() == 1 {
		cfg.root = flags.Arg(0)
	} else {
		cfg.root = "."
	}
	bindIP := net.ParseIP(cfg.bind)
	if bindIP == nil {
		return serveConfig{}, fmt.Errorf("--bind must be an IP address, got %q", cfg.bind)
	}
	if cfg.port < 0 || cfg.port > 65535 {
		return serveConfig{}, fmt.Errorf("--port must be between 0 and 65535")
	}
	if err := validatePathTokenOption(cfg.pathToken); err != nil {
		return serveConfig{}, err
	}
	editorSelections := 0
	if cfg.editCommand != "" {
		editorSelections++
	}
	if cfg.editSublime {
		editorSelections++
	}
	if cfg.editVSCode {
		editorSelections++
	}
	if editorSelections > 1 {
		return serveConfig{}, errors.New("--edit-command, --edit-sublime, and --edit-vscode are mutually exclusive")
	}
	if len(cfg.editArgs) > 0 && editorSelections == 0 {
		return serveConfig{}, errors.New("--edit-arg requires an editor option")
	}
	if editorSelections > 0 && !bindIP.IsLoopback() {
		return serveConfig{}, errors.New("editor integration requires a loopback --bind address")
	}
	return cfg, nil
}

func parseSaveFlags(args []string, output io.Writer) (saveConfig, error) {
	var cfg saveConfig
	flags := newFlagSet("mdfmt save", output,
		"Usage: mdfmt save SOURCE [-o|--output TARGET] [--hash] [-q|--quiet]",
		"Render one source file as a standalone HTML document.")
	flags.StringVarP(&cfg.output, "output", "o", "", "output file or existing directory")
	flags.BoolVar(&cfg.hash, "hash", false, "use a stable opaque output filename")
	flags.BoolVarP(&cfg.quiet, "quiet", "q", false, "do not print the output path")
	if err := flags.Parse(args); err != nil {
		return saveConfig{}, err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return saveConfig{}, fmt.Errorf("expected exactly one SOURCE")
	}
	cfg.source = flags.Arg(0)
	return cfg, nil
}

func parseBuildFlags(args []string, output io.Writer) (buildConfig, error) {
	cfg := buildConfig{pathToken: pathTokenNone}
	flags := newFlagSet("mdfmt build", output,
		"Usage: mdfmt build SOURCE_DIR -o TARGET_DIR [options]\n       mdfmt build --mount MOUNT=SOURCE_DIR [--mount ...] -o TARGET_DIR [options]",
		"Build one or more Markdown directory trees as a static site.")
	flags.StringArrayVar(&cfg.mounts, "mount", nil, "mount mapping MOUNT_PATH=SOURCE_DIR (repeatable)")
	flags.StringVarP(&cfg.output, "output", "o", "", "owned output directory (required)")
	flags.StringVar(&cfg.pathToken, "path-token", pathTokenNone, "URL path token: auto, none, or a custom value")
	flags.BoolVar(&cfg.strict, "strict", false, "treat build warnings as errors")
	flags.BoolVar(&cfg.strictNoDirIndex, "strict-no-dir-index", false, "require index.md in every source directory")
	flags.BoolVarP(&cfg.quiet, "quiet", "q", false, "do not print the generated site path")
	if err := flags.Parse(args); err != nil {
		return buildConfig{}, err
	}
	if cfg.output == "" {
		flags.Usage()
		return buildConfig{}, errors.New("--output is required")
	}
	if len(cfg.mounts) > 0 {
		if flags.NArg() != 0 {
			flags.Usage()
			return buildConfig{}, errors.New("a positional SOURCE_DIR cannot be combined with --mount")
		}
	} else {
		if flags.NArg() != 1 {
			flags.Usage()
			return buildConfig{}, errors.New("expected exactly one SOURCE_DIR or at least one --mount")
		}
		cfg.source = flags.Arg(0)
	}
	if err := validatePathTokenOption(cfg.pathToken); err != nil {
		return buildConfig{}, err
	}
	return cfg, nil
}

func printRootUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: mdfmt <command> [options]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  serve   Serve a directory of Markdown files")
	fmt.Fprintln(output, "  open    Open served Markdown files and directories")
	fmt.Fprintln(output, "  config  Show remembered roots, ports, and listening PIDs")
	fmt.Fprintln(output, "  save    Render one source file as standalone HTML")
	fmt.Fprintln(output, "  build   Build Markdown directories as a static site")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run \"mdfmt <command> --help\" for command-specific help.")
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return fmt.Errorf("expected a command")
	}

	switch args[0] {
	case "help", "-h", "--help":
		printRootUsage(stdout)
		return nil
	case "serve":
		cfg, err := parseServeFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return runServe(cfg, stdout, log.New(stderr, "mdfmt: ", log.LstdFlags))
	case "open":
		cfg, err := parseOpenFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return runOpen(cfg, stdout)
	case "config":
		if err := parseConfigFlags(args[1:], stderr); err != nil {
			return err
		}
		return runConfig(stdout)
	case "save":
		cfg, err := parseSaveFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return runSave(cfg, stdout)
	case "build":
		cfg, err := parseBuildFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return runBuild(cfg, stdout, stderr)
	default:
		printRootUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runBuild(cfg buildConfig, stdout, stderr io.Writer) error {
	outputPath, err := buildStaticSite(cfg, stderr)
	if err != nil {
		return err
	}
	if !cfg.quiet {
		fmt.Fprintln(stdout, outputPath)
	}
	return nil
}

func runSave(cfg saveConfig, stdout io.Writer) error {
	outputPath, err := saveStandalone(cfg)
	if err != nil {
		return err
	}
	if !cfg.quiet {
		fmt.Fprintln(stdout, outputPath)
	}
	return nil
}

func runServe(cfg serveConfig, stdout io.Writer, logger *log.Logger) error {
	handler, err := newMarkdownServer(cfg.root, logger)
	if err != nil {
		return err
	}
	editor, err := newEditorLauncher(cfg, handler.root, stdout, logger)
	if err != nil {
		return err
	}
	handler.editor = editor

	listener, err := listenForServe(cfg, handler.root)
	if err != nil {
		return err
	}
	handler.pathToken = listener.pathToken
	if handler.editor != nil {
		handler.editor.allowedHost = listener.Addr().String()
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Printf("serving %s at http://%s%s", handler.root, listener.Addr(), servedURL(listener.pathToken, nil, true))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, pflag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "mdfmt: %v\n", err)
			os.Exit(1)
		}
	}
}
