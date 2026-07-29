package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const hashedNameSalt = "mdfmt:standalone-output-name:v1"

// standalonePageData reuses pageData so both commands render from one document
// model; the extra fields carry the assets a self-contained file must inline.
type standalonePageData struct {
	pageData
	Stylesheet template.CSS
	Script     template.JS
	CSP        string
}

func saveStandalone(cfg saveConfig) (string, error) {
	sourcePath, sourceInfo, source, err := readStandaloneSource(cfg.source)
	if err != nil {
		return "", err
	}
	sourceName := filepath.Base(cfg.source)
	targetPath, err := standaloneTarget(cfg, sourcePath, sourceName)
	if err != nil {
		return "", err
	}
	if err := rejectSourceTargetIdentity(sourcePath, sourceInfo, targetPath); err != nil {
		return "", err
	}

	output, err := renderStandalone(source, sourceName, sourceInfo)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(targetPath, output, 0o644); err != nil {
		return "", fmt.Errorf("write output: %w", err)
	}
	return targetPath, nil
}

func readStandaloneSource(source string) (string, fs.FileInfo, []byte, error) {
	if source == "" {
		return "", nil, nil, errors.New("SOURCE is required")
	}
	resolved, info, err := resolveExisting(source)
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolve source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, nil, fmt.Errorf("source is not a regular file: %s", source)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read source: %w", err)
	}
	return resolved, info, content, nil
}

func standaloneTarget(cfg saveConfig, sourcePath, sourceName string) (string, error) {
	generatedName := readableHTMLName(sourceName)
	if cfg.hash {
		generatedName = hashedHTMLName(sourcePath)
	}

	if cfg.output == "" {
		directory := filepath.Join(os.TempDir(), "mdfmt")
		if err := ensurePrivateOutputDirectory(directory); err != nil {
			return "", err
		}
		return filepath.Abs(filepath.Join(directory, generatedName))
	}

	absolute, err := filepath.Abs(cfg.output)
	if err != nil {
		return "", fmt.Errorf("resolve output: %w", err)
	}
	info, statErr := os.Stat(absolute)
	switch {
	case statErr == nil && info.IsDir():
		return filepath.Join(absolute, generatedName), nil
	case statErr == nil && !info.Mode().IsRegular():
		return "", fmt.Errorf("output is neither a regular file nor directory: %s", cfg.output)
	case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
		return "", fmt.Errorf("stat output: %w", statErr)
	case statErr != nil && strings.HasSuffix(cfg.output, string(filepath.Separator)):
		return "", fmt.Errorf("output directory does not exist: %s", cfg.output)
	}

	// --output names a file, whether or not it exists yet.
	if cfg.hash {
		return "", errors.New("--hash cannot be used when --output names a file")
	}
	if statErr != nil {
		parent := filepath.Dir(absolute)
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return "", fmt.Errorf("stat output directory: %w", err)
		}
		if !parentInfo.IsDir() {
			return "", fmt.Errorf("output parent is not a directory: %s", parent)
		}
	}
	return absolute, nil
}

func ensurePrivateOutputDirectory(directory string) error {
	err := os.Mkdir(directory, 0o700)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create temporary output directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("stat temporary output directory: %w", err)
	}
	// Lstat reports a symlink as a non-directory, so this also rejects one.
	if !info.IsDir() {
		return fmt.Errorf("temporary output path is not a private directory: %s", directory)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure temporary output directory: %w", err)
		}
	}
	return nil
}

func readableHTMLName(sourceName string) string {
	name := stem(sourceName)
	if name == "" {
		name = sourceName
	}
	return name + ".html"
}

func hashedHTMLName(canonicalSourcePath string) string {
	digest := sha256.Sum256([]byte(hashedNameSalt + "\x00" + canonicalSourcePath))
	return hex.EncodeToString(digest[:12]) + ".html"
}

func rejectSourceTargetIdentity(sourcePath string, sourceInfo fs.FileInfo, targetPath string) error {
	targetInfo, err := os.Stat(targetPath)
	if err == nil && os.SameFile(sourceInfo, targetInfo) {
		return errors.New("output resolves to the source file")
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat output: %w", err)
	}

	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(targetPath))
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if filepath.Join(resolvedParent, filepath.Base(targetPath)) == sourcePath {
		return errors.New("output resolves to the source file")
	}
	return nil
}

func renderStandalone(source []byte, filename string, info fs.FileInfo) ([]byte, error) {
	rendered, err := renderMarkdownDocument(newGoldmark(), source, filename)
	if err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}
	data := standalonePageData{
		pageData: pageData{
			Title: rendered.Title,
			Breadcrumbs: []breadcrumb{{
				Name: filename,
				Meta: fileBreadcrumbMeta(info, time.Now()),
			}},
			Body:       rendered.Body,
			TOC:        rendered.TOC,
			IsDocument: true,
			ShowTitle:  !rendered.HasH1,
		},
		Stylesheet: template.CSS(stylesheetAsset),
		Script:     template.JS(scriptAsset),
		CSP:        standaloneCSP(stylesheetAsset, scriptAsset),
	}
	var output bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&output, "standalone.html", data); err != nil {
		return nil, fmt.Errorf("execute standalone template: %w", err)
	}
	return output.Bytes(), nil
}

func standaloneCSP(stylesheet, script []byte) string {
	return contentSecurityPolicy(sha256Source(stylesheet), sha256Source(script), "'none'", false)
}

func sha256Source(content []byte) string {
	digest := sha256.Sum256(content)
	return "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
}

func writeFileAtomic(target string, content []byte, mode fs.FileMode) (err error) {
	parent := filepath.Dir(target)
	temp, err := os.CreateTemp(parent, ".mdfmt-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = os.Remove(tempName)
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(content); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tempName, target); err != nil {
		return err
	}
	return nil
}
