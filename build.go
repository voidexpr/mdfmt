package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
)

const buildMarker = "mdfmt-build:v1\n"

type buildMount struct {
	name        string
	components  []string
	root        string
	directories map[string]*buildDirectory
	documents   map[string]*buildDocument
}

type buildDirectory struct {
	mount      *buildMount
	rel        []string
	sourcePath string
	info       fs.FileInfo
	children   []*buildDirectory
	documents  []*buildDocument
	landing    *buildDocument
}

type buildDocument struct {
	mount      *buildMount
	directory  *buildDirectory
	rel        []string
	sourcePath string
	info       fs.FileInfo
	source     []byte
	title      string
	output     []string
}

type buildDiagnostics struct {
	strict   bool
	warnings []string
}

func (d *buildDiagnostics) warn(format string, args ...any) {
	d.warnings = append(d.warnings, fmt.Sprintf(format, args...))
}

func (d *buildDiagnostics) err() error {
	if !d.strict || len(d.warnings) == 0 {
		return nil
	}
	return fmt.Errorf("strict build failed:\n  %s", strings.Join(d.warnings, "\n  "))
}

type staticSiteBuilder struct {
	cfg         buildConfig
	mounts      []*buildMount
	collection  bool
	markdown    goldmark.Markdown
	diagnostics buildDiagnostics
	workRoot    string
	siteRoot    string
	media       map[string][]string
	now         time.Time
	fatalErr    error
}

func buildStaticSite(cfg buildConfig, warningOutput io.Writer) (string, error) {
	builder := &staticSiteBuilder{
		cfg:         cfg,
		collection:  len(cfg.mounts) > 0,
		markdown:    newGoldmark(),
		diagnostics: buildDiagnostics{strict: cfg.strict},
		media:       make(map[string][]string),
		now:         time.Now(),
	}

	target, err := validateBuildTargetPath(cfg.output)
	if err != nil {
		return "", err
	}
	builder.mounts, err = resolveBuildMounts(cfg)
	if err != nil {
		return "", err
	}
	if err := validateBuildPathRelationships(target, builder.mounts); err != nil {
		return "", err
	}

	token := ""
	switch cfg.pathToken {
	case pathTokenNone:
	case pathTokenAuto:
		token, err = generatePathToken()
		if err != nil {
			return "", err
		}
	default:
		token = cfg.pathToken
	}

	workRoot, unlock, err := acquireBuildWorkspace()
	if err != nil {
		return "", err
	}
	defer unlock()
	builder.workRoot = workRoot
	builder.siteRoot = workRoot
	if token != "" {
		builder.siteRoot = filepath.Join(workRoot, token)
	}
	if err := os.MkdirAll(builder.siteRoot, 0o755); err != nil {
		return "", fmt.Errorf("create build site root: %w", err)
	}

	for _, mount := range builder.mounts {
		if err := builder.inventoryMount(mount); err != nil {
			return "", err
		}
		builder.assignRoutes(mount)
	}
	if cfg.strictNoDirIndex {
		var missing []string
		for _, mount := range builder.mounts {
			for _, directory := range sortedBuildDirectories(mount) {
				if directory.landing == nil {
					missing = append(missing, builder.sourceLabel(mount, directory.rel)+"/index.md")
				}
			}
		}
		if len(missing) > 0 {
			return "", fmt.Errorf("--strict-no-dir-index: missing index.md:\n  %s", strings.Join(missing, "\n  "))
		}
	}
	if err := builder.diagnostics.err(); err != nil {
		return "", err
	}
	if err := builder.writeAssets(); err != nil {
		return "", err
	}
	if builder.collection {
		if err := builder.writeCollectionHub(); err != nil {
			return "", err
		}
	}
	for _, mount := range builder.mounts {
		for _, directory := range sortedBuildDirectories(mount) {
			if err := builder.writeDirectory(directory); err != nil {
				return "", err
			}
			for _, document := range directory.documents {
				if document != directory.landing {
					if err := builder.writeDocument(document, false); err != nil {
						return "", err
					}
				}
			}
		}
	}
	if err := builder.diagnostics.err(); err != nil {
		return "", err
	}
	for _, warning := range builder.diagnostics.warnings {
		fmt.Fprintf(warningOutput, "mdfmt: warning: %s\n", warning)
	}
	if err := publishBuildTarget(target, workRoot); err != nil {
		return "", err
	}
	sitePath := target
	if token != "" {
		sitePath = filepath.Join(target, token)
	}
	return sitePath, nil
}

func resolveBuildMounts(cfg buildConfig) ([]*buildMount, error) {
	if cfg.source != "" {
		root, info, err := resolveExisting(cfg.source)
		if err != nil {
			return nil, fmt.Errorf("resolve source: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("source is not a directory: %s", cfg.source)
		}
		return []*buildMount{{name: filepath.Base(root), root: root}}, nil
	}

	mounts := make([]*buildMount, 0, len(cfg.mounts))
	for _, value := range cfg.mounts {
		name, source, ok := strings.Cut(value, "=")
		if !ok || name == "" || source == "" {
			return nil, fmt.Errorf("--mount must be MOUNT_PATH=SOURCE_DIR, got %q", value)
		}
		components, err := validateBuildMountPath(name)
		if err != nil {
			return nil, err
		}
		root, info, err := resolveExisting(source)
		if err != nil {
			return nil, fmt.Errorf("resolve mount %q: %w", name, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("mount %q source is not a directory", name)
		}
		mounts = append(mounts, &buildMount{name: name, components: components, root: root})
	}
	for i, left := range mounts {
		for _, right := range mounts[:i] {
			if componentPrefixFold(left.components, right.components) || componentPrefixFold(right.components, left.components) {
				return nil, fmt.Errorf("mount paths %q and %q overlap", right.name, left.name)
			}
			if withinRoot(left.root, right.root) || withinRoot(right.root, left.root) {
				return nil, fmt.Errorf("mount source roots %q and %q overlap", right.name, left.name)
			}
		}
	}
	return mounts, nil
}

func validateBuildMountPath(name string) ([]string, error) {
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return nil, fmt.Errorf("invalid mount path %q", name)
	}
	components := strings.Split(name, "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." || strings.EqualFold(component, "_mdfmt") || strings.ContainsAny(component, "\\\x00") {
			return nil, fmt.Errorf("invalid mount path %q", name)
		}
	}
	return components, nil
}

func componentPrefixFold(prefix, value []string) bool {
	if len(prefix) > len(value) {
		return false
	}
	for i := range prefix {
		if !strings.EqualFold(prefix[i], value[i]) {
			return false
		}
	}
	return true
}

func (b *staticSiteBuilder) inventoryMount(mount *buildMount) error {
	mount.directories = make(map[string]*buildDirectory)
	mount.documents = make(map[string]*buildDocument)
	activeDirectories := make(map[string]bool)
	var walk func(string, []string) (*buildDirectory, error)
	walk = func(directoryPath string, rel []string) (*buildDirectory, error) {
		resolved, err := filepath.EvalSymlinks(directoryPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", b.sourceLabel(mount, rel), err)
		}
		if !withinRoot(mount.root, resolved) || hasHiddenComponent(mount.root, resolved) {
			return nil, fmt.Errorf("directory escapes source root: %s", b.sourceLabel(mount, rel))
		}
		if activeDirectories[resolved] {
			b.diagnostics.warn("%s: directory symlink cycle skipped", b.sourceLabel(mount, rel))
			return nil, nil
		}
		activeDirectories[resolved] = true
		defer delete(activeDirectories, resolved)
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", b.sourceLabel(mount, rel), err)
		}
		directory := &buildDirectory{mount: mount, rel: cloneComponents(rel), sourcePath: resolved, info: info}
		mount.directories[componentKey(rel)] = directory
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return nil, fmt.Errorf("read directory %s: %w", b.sourceLabel(mount, rel), err)
		}
		sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			entryRel := appendComponents(rel, name)
			candidate := filepath.Join(resolved, name)
			entryResolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				b.diagnostics.warn("%s: unreadable entry skipped", b.sourceLabel(mount, entryRel))
				continue
			}
			if !withinRoot(mount.root, entryResolved) || hasHiddenComponent(mount.root, entryResolved) {
				b.diagnostics.warn("%s: escaping or hidden symlink skipped", b.sourceLabel(mount, entryRel))
				continue
			}
			entryInfo, err := os.Stat(entryResolved)
			if err != nil {
				b.diagnostics.warn("%s: unreadable entry skipped", b.sourceLabel(mount, entryRel))
				continue
			}
			switch {
			case entryInfo.IsDir():
				child, err := walk(candidate, entryRel)
				if err != nil {
					return nil, err
				}
				if child != nil {
					directory.children = append(directory.children, child)
				}
			case entryInfo.Mode().IsRegular() && isMarkdownName(name) && isMarkdownName(entryInfo.Name()):
				source, err := os.ReadFile(entryResolved)
				if err != nil {
					b.diagnostics.warn("%s: unreadable Markdown file skipped", b.sourceLabel(mount, entryRel))
					continue
				}
				document := &buildDocument{
					mount: mount, directory: directory, rel: entryRel, sourcePath: entryResolved,
					info: entryInfo, source: source, title: extractTitle(b.markdown, source, name),
				}
				directory.documents = append(directory.documents, document)
				mount.documents[componentKey(entryRel)] = document
			}
		}
		seenChildren := make(map[string]string)
		for _, child := range directory.children {
			name := child.rel[len(child.rel)-1]
			folded := strings.ToLower(name)
			if previous := seenChildren[folded]; previous != "" {
				return nil, fmt.Errorf("%s: directory names %q and %q collide case-insensitively", b.sourceLabel(mount, rel), previous, name)
			}
			seenChildren[folded] = name
			if !b.collection && len(rel) == 0 && strings.EqualFold(name, "_mdfmt") {
				return nil, errors.New("source directory _mdfmt conflicts with the reserved build asset namespace")
			}
		}
		return directory, nil
	}
	_, err := walk(mount.root, nil)
	return err
}

func (b *staticSiteBuilder) assignRoutes(mount *buildMount) {
	for _, directory := range sortedBuildDirectories(mount) {
		sort.SliceStable(directory.documents, func(i, j int) bool {
			return strings.ToLower(directory.documents[i].rel[len(directory.documents[i].rel)-1]) < strings.ToLower(directory.documents[j].rel[len(directory.documents[j].rel)-1])
		})
		var landingCandidates []*buildDocument
		for _, document := range directory.documents {
			if strings.EqualFold(document.rel[len(document.rel)-1], "index.md") {
				landingCandidates = append(landingCandidates, document)
			}
		}
		if len(landingCandidates) > 0 {
			directory.landing = landingCandidates[0]
			for _, candidate := range landingCandidates {
				if candidate.rel[len(candidate.rel)-1] == "index.md" {
					directory.landing = candidate
					break
				}
			}
			directory.landing.output = appendComponents(concatComponents(mount.components, directory.rel), "index.html")
		}
		if len(landingCandidates) > 1 {
			b.diagnostics.warn("%s: multiple case variants of index.md collide", b.sourceLabel(mount, directory.rel))
		}

		groups := make(map[string][]*buildDocument)
		childNames := make(map[string]bool)
		for _, child := range directory.children {
			childNames[strings.ToLower(child.rel[len(child.rel)-1])] = true
		}
		for _, document := range directory.documents {
			if document == directory.landing {
				continue
			}
			name := document.rel[len(document.rel)-1]
			candidate := stem(name) + ".html"
			groups[strings.ToLower(candidate)] = append(groups[strings.ToLower(candidate)], document)
		}
		groupNames := make([]string, 0, len(groups))
		for candidate := range groups {
			groupNames = append(groupNames, candidate)
		}
		sort.Strings(groupNames)
		for _, candidate := range groupNames {
			documents := groups[candidate]
			collision := len(documents) > 1 || candidate == "index.html" || childNames[candidate]
			if collision {
				for _, document := range documents {
					b.diagnostics.warn("%s: output route collision for %s", b.sourceLabel(mount, document.rel), candidate)
				}
			}
			for _, document := range documents {
				name := document.rel[len(document.rel)-1]
				outputName := stem(name) + ".html"
				if collision {
					outputName = name + ".html"
				}
				document.output = appendComponents(concatComponents(mount.components, directory.rel), outputName)
			}
		}

		used := map[string]*buildDocument{}
		for _, document := range directory.documents {
			if document == directory.landing {
				continue
			}
			name := strings.ToLower(document.output[len(document.output)-1])
			if previous := used[name]; previous != nil || childNames[name] {
				digest := sha256.Sum256([]byte(strings.Join(document.rel, "/")))
				base := strings.TrimSuffix(document.output[len(document.output)-1], ".html")
				document.output[len(document.output)-1] = fmt.Sprintf("%s-%s.html", base, hex.EncodeToString(digest[:4]))
				b.diagnostics.warn("%s: output route required a deterministic suffix", b.sourceLabel(mount, document.rel))
			}
			used[strings.ToLower(document.output[len(document.output)-1])] = document
		}
	}
}

func sortedBuildDirectories(mount *buildMount) []*buildDirectory {
	directories := make([]*buildDirectory, 0, len(mount.directories))
	for _, directory := range mount.directories {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(i, j int) bool { return componentKey(directories[i].rel) < componentKey(directories[j].rel) })
	return directories
}

func (b *staticSiteBuilder) writeAssets() error {
	assets := []struct {
		name    string
		content []byte
	}{
		{"style.css", stylesheetAsset},
		{"app.js", scriptAsset},
		{"favicon.svg", faviconSVGAsset},
		{"favicon.ico", faviconICOAsset},
		{"favicon-16.png", favicon16Asset},
		{"favicon-32.png", favicon32Asset},
		{"favicon-48.png", favicon48Asset},
		{"apple-touch-icon.png", appleTouchIconAsset},
	}
	for _, asset := range assets {
		if err := writeBuildFile(filepath.Join(b.siteRoot, "_mdfmt", asset.name), asset.content); err != nil {
			return fmt.Errorf("write asset %s: %w", asset.name, err)
		}
	}
	return nil
}

func (b *staticSiteBuilder) writeCollectionHub() error {
	base := []string{}
	projects := b.projectEntries(base, nil)
	data := pageData{
		Title: "Projects", Directory: "Projects", Breadcrumbs: []breadcrumb{{Name: "Projects"}},
		Directories: projects, Projects: projects, ShowTitle: true, StaticCSP: staticBuildCSP(),
	}
	b.setStaticAssetURLs(&data, base)
	return b.writePage([]string{"index.html"}, data)
}

func (b *staticSiteBuilder) writeDirectory(directory *buildDirectory) error {
	if directory.landing != nil {
		return b.writeDocument(directory.landing, true)
	}
	base := concatComponents(directory.mount.components, directory.rel)
	directories, files := b.directoryNavigation(directory, "", base)
	data := pageData{
		Title: directoryDisplayName(directory), Directory: directoryDisplayName(directory),
		Breadcrumbs: b.buildBreadcrumbs(directory.mount, directory.rel, nil, base),
		Parent:      b.buildParentEntry(directory, base), Directories: directories, Files: files,
		Projects: b.projectEntries(base, directory.mount), ShowTitle: true, StaticCSP: staticBuildCSP(),
	}
	b.setStaticAssetURLs(&data, base)
	return b.writePage(appendComponents(base, "index.html"), data)
}

func (b *staticSiteBuilder) writeDocument(document *buildDocument, landing bool) error {
	base := concatComponents(document.mount.components, document.directory.rel)
	rendered, err := b.renderDocument(document, base)
	if err != nil {
		return fmt.Errorf("render %s: %w", b.sourceLabel(document.mount, document.rel), err)
	}
	directories, files := b.directoryNavigation(document.directory, filepath.Base(document.sourcePath), base)
	var documentCrumb *breadcrumb
	if !landing {
		documentCrumb = &breadcrumb{
			Name: document.rel[len(document.rel)-1], ModifiedFull: document.info.ModTime().Format(time.RFC3339),
			Modified: document.info.ModTime().Local().Format("Jan 2, 2006 3:04 PM"),
			Ago:      humanAgoCompact(document.info.ModTime(), b.now), Size: humanSize(document.info.Size()),
		}
	}
	data := pageData{
		Title: rendered.Title, Directory: directoryDisplayName(document.directory),
		Breadcrumbs: b.buildBreadcrumbs(document.mount, document.directory.rel, documentCrumb, base),
		Parent:      b.buildParentEntry(document.directory, base), Directories: directories, Files: files,
		Body: rendered.Body, TOC: rendered.TOC, TopURL: template.URL(relativeURL(base, document.output, false)),
		IsDocument: true, ShowTitle: !rendered.HasH1, Projects: b.projectEntries(base, document.mount),
		StaticCSP: staticBuildCSP(),
	}
	if landing {
		data.TopURL = template.URL(staticDirectoryURL(base, base))
	}
	b.setStaticAssetURLs(&data, base)
	return b.writePage(document.output, data)
}

func (b *staticSiteBuilder) renderDocument(document *buildDocument, base []string) (renderedDocument, error) {
	rewriter := func(destination []byte, image bool) []byte {
		return b.rewriteDestination(document, base, destination, image)
	}
	rendered, err := renderMarkdownDocumentWithDestinationRewriter(b.markdown, document.source, document.sourcePath, rewriter)
	if err == nil && b.fatalErr != nil {
		err = b.fatalErr
	}
	return rendered, err
}

func (b *staticSiteBuilder) rewriteDestination(document *buildDocument, base []string, destination []byte, image bool) []byte {
	raw := string(destination)
	target, err := url.Parse(raw)
	if err != nil || target.IsAbs() || target.Host != "" || strings.HasPrefix(raw, "//") {
		return destination
	}
	if target.Path == "" {
		return destination
	}
	componentsFromURL, absolute, err := decodeBuildURLPath(target.EscapedPath())
	if err != nil {
		b.diagnostics.warn("%s: malformed local URL %q", b.sourceLabel(document.mount, document.rel), raw)
		return destination
	}
	var components []string
	if absolute {
		components = componentsFromURL
	} else {
		components = concatComponents(document.directory.rel, componentsFromURL)
	}
	components, ok := cleanBuildComponents(components)
	if !ok {
		b.diagnostics.warn("%s: local URL escapes its mount: %q", b.sourceLabel(document.mount, document.rel), raw)
		return destination
	}

	var rewritten string
	if image {
		assetRoute, ok := b.copyReferencedImage(document.mount, components)
		if !ok {
			b.diagnostics.warn("%s: unavailable local image %q", b.sourceLabel(document.mount, document.rel), raw)
			return destination
		}
		rewritten = relativeURL(base, assetRoute, false)
	} else if len(components) > 0 && isImageName(components[len(components)-1]) {
		assetRoute, ok := b.copyReferencedImage(document.mount, components)
		if !ok {
			b.diagnostics.warn("%s: unavailable linked image %q", b.sourceLabel(document.mount, document.rel), raw)
			return destination
		}
		rewritten = relativeURL(base, assetRoute, false)
	} else if targetDirectory := document.mount.directories[componentKey(components)]; targetDirectory != nil {
		rewritten = staticDirectoryURL(base, concatComponents(document.mount.components, targetDirectory.rel))
	} else if targetDocument := document.mount.documents[componentKey(components)]; targetDocument != nil {
		rewritten = relativeURL(base, targetDocument.output, false)
	} else {
		b.diagnostics.warn("%s: broken local document link %q", b.sourceLabel(document.mount, document.rel), raw)
		return destination
	}
	if target.ForceQuery || target.RawQuery != "" {
		rewritten += "?" + target.RawQuery
	}
	if target.Fragment != "" {
		rewritten += "#" + target.EscapedFragment()
	}
	return []byte(rewritten)
}

func (b *staticSiteBuilder) copyReferencedImage(mount *buildMount, rel []string) ([]string, bool) {
	if len(rel) == 0 || !isImageName(rel[len(rel)-1]) {
		return nil, false
	}
	candidate := filepath.Join(append([]string{mount.root}, rel...)...)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !withinRoot(mount.root, resolved) || hasHiddenComponent(mount.root, resolved) {
		return nil, false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || !isImageName(info.Name()) {
		return nil, false
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, false
	}
	digest := sha256.Sum256(content)
	key := hex.EncodeToString(digest[:])
	if route := b.media[key]; route != nil {
		return route, true
	}
	extension := strings.ToLower(filepath.Ext(resolved))
	route := []string{"_mdfmt", "media", key[:24] + extension}
	if err := writeBuildFile(filepath.Join(append([]string{b.siteRoot}, route...)...), content); err != nil {
		b.fatalErr = fmt.Errorf("write referenced image: %w", err)
		return nil, false
	}
	b.media[key] = route
	return route, true
}

func decodeBuildURLPath(escaped string) ([]string, bool, error) {
	absolute := strings.HasPrefix(escaped, "/")
	value := strings.TrimPrefix(escaped, "/")
	if value == "" {
		return nil, absolute, nil
	}
	parts := strings.Split(value, "/")
	components := make([]string, 0, len(parts))
	for i, part := range parts {
		if part == "" && i != len(parts)-1 {
			return nil, false, errors.New("empty URL path component")
		}
		if part == "" {
			continue
		}
		component, err := url.PathUnescape(part)
		if err != nil || strings.ContainsAny(component, "/\\\x00") {
			return nil, false, errors.New("invalid escaped URL path component")
		}
		components = append(components, component)
	}
	return components, absolute, nil
}

func cleanBuildComponents(components []string) ([]string, bool) {
	cleaned := make([]string, 0, len(components))
	for _, component := range components {
		switch component {
		case "", ".":
		case "..":
			if len(cleaned) == 0 {
				return nil, false
			}
			cleaned = cleaned[:len(cleaned)-1]
		default:
			cleaned = append(cleaned, component)
		}
	}
	return cleaned, true
}

func (b *staticSiteBuilder) directoryNavigation(directory *buildDirectory, active string, base []string) ([]navEntry, []navEntry) {
	directories := make([]navEntry, 0, len(directory.children))
	for _, child := range directory.children {
		target := concatComponents(directory.mount.components, child.rel)
		directories = append(directories, navEntry{Name: child.rel[len(child.rel)-1], Title: child.rel[len(child.rel)-1], URL: template.URL(staticDirectoryURL(base, target)), IsDir: true})
	}
	files := make([]navEntry, 0, len(directory.documents))
	for _, document := range directory.documents {
		if document == directory.landing {
			continue
		}
		files = append(files, navEntry{
			Name: document.rel[len(document.rel)-1], Title: document.title,
			URL: template.URL(relativeURL(base, document.output, false)), Active: document.rel[len(document.rel)-1] == active,
			Modified: document.info.ModTime().Format("Jan 2, 2006 15:04"), ModifiedFull: document.info.ModTime().Format(time.RFC3339),
			Ago: humanAgo(document.info.ModTime(), b.now), Size: humanSize(document.info.Size()),
			SortModified: document.info.ModTime().UnixNano(), SortSize: document.info.Size(),
		})
	}
	sortEntries(directories)
	sortEntries(files)
	return directories, files
}

func (b *staticSiteBuilder) buildBreadcrumbs(mount *buildMount, rel []string, document *breadcrumb, base []string) []breadcrumb {
	var crumbs []breadcrumb
	if b.collection {
		crumbs = append(crumbs, breadcrumb{Name: "Projects", URL: template.URL(staticDirectoryURL(base, nil))})
		crumbs = append(crumbs, breadcrumb{Name: mount.name, URL: template.URL(staticDirectoryURL(base, mount.components))})
	} else {
		crumbs = append(crumbs, breadcrumb{Name: mount.name, URL: template.URL(staticDirectoryURL(base, nil))})
	}
	for i, component := range rel {
		target := concatComponents(mount.components, rel[:i+1])
		crumbs = append(crumbs, breadcrumb{Name: component, URL: template.URL(staticDirectoryURL(base, target))})
	}
	if document != nil {
		crumbs = append(crumbs, *document)
	}
	return crumbs
}

func (b *staticSiteBuilder) buildParentEntry(directory *buildDirectory, base []string) *navEntry {
	if len(directory.rel) > 0 {
		target := concatComponents(directory.mount.components, directory.rel[:len(directory.rel)-1])
		return &navEntry{Name: "Parent directory", Title: "Parent directory", URL: template.URL(staticDirectoryURL(base, target)), IsDir: true}
	}
	if b.collection {
		return &navEntry{Name: "All projects", Title: "All projects", URL: template.URL(staticDirectoryURL(base, nil)), IsDir: true}
	}
	return nil
}

func (b *staticSiteBuilder) projectEntries(base []string, active *buildMount) []navEntry {
	if !b.collection {
		return nil
	}
	projects := make([]navEntry, 0, len(b.mounts))
	for _, mount := range b.mounts {
		projects = append(projects, navEntry{Name: mount.name, Title: mount.name, URL: template.URL(staticDirectoryURL(base, mount.components)), IsDir: true, Active: mount == active})
	}
	return projects
}

func (b *staticSiteBuilder) setStaticAssetURLs(data *pageData, base []string) {
	asset := func(name string) template.URL {
		return template.URL(relativeURL(base, []string{"_mdfmt", name}, false))
	}
	data.StylesheetURL = asset("style.css")
	data.ScriptURL = asset("app.js")
	data.FaviconSVGURL = asset("favicon.svg")
	data.FaviconICOURL = asset("favicon.ico")
	data.Favicon16URL = asset("favicon-16.png")
	data.Favicon32URL = asset("favicon-32.png")
	data.Favicon48URL = asset("favicon-48.png")
	data.AppleIconURL = asset("apple-touch-icon.png")
}

func staticDirectoryURL(base, directory []string) string {
	return relativeURL(base, appendComponents(cloneComponents(directory), "index.html"), false)
}

func (b *staticSiteBuilder) writePage(route []string, data pageData) error {
	var output bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&output, "page.html", data); err != nil {
		return fmt.Errorf("execute page template: %w", err)
	}
	return writeBuildFile(filepath.Join(append([]string{b.siteRoot}, route...)...), output.Bytes())
}

func staticBuildCSP() string {
	return contentSecurityPolicy("'self'", "'self'", "'self' data:", false)
}

func directoryDisplayName(directory *buildDirectory) string {
	if len(directory.rel) > 0 {
		return directory.rel[len(directory.rel)-1]
	}
	return directory.mount.name
}

func (b *staticSiteBuilder) sourceLabel(mount *buildMount, rel []string) string {
	value := strings.Join(rel, "/")
	if value == "" {
		value = "."
	}
	if b.collection {
		return mount.name + ":" + value
	}
	return value
}

func componentKey(components []string) string { return strings.Join(components, "\x00") }

func cloneComponents(components []string) []string { return append([]string(nil), components...) }

func concatComponents(left, right []string) []string {
	result := make([]string, 0, len(left)+len(right))
	result = append(result, left...)
	result = append(result, right...)
	return result
}

func writeBuildFile(filename string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filename, content, 0o644)
}

func validateBuildTargetPath(output string) (string, error) {
	absolute, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve output: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("output must not be a symlink: %s", absolute)
	} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("stat output: %w", statErr)
	}
	absolute, err = resolveProspectivePath(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve output: %w", err)
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		if resolvedHome, resolveErr := filepath.EvalSymlinks(home); resolveErr == nil {
			home = resolvedHome
		}
	}
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) || (home != "" && absolute == filepath.Clean(home)) {
		return "", fmt.Errorf("unsafe output directory: %s", absolute)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("output must be a real directory: %s", absolute)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat output: %w", err)
	}
	return absolute, nil
}

func resolveProspectivePath(absolute string) (string, error) {
	current := absolute
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", absolute)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func validateBuildPathRelationships(target string, mounts []*buildMount) error {
	temporary := filepath.Join(os.TempDir(), "mdfmt", "build")
	if resolved, err := resolveProspectivePath(temporary); err == nil {
		temporary = resolved
	}
	for _, mount := range mounts {
		if withinRoot(mount.root, target) || withinRoot(target, mount.root) {
			return fmt.Errorf("output and source overlap for mount %q", mount.name)
		}
		if withinRoot(mount.root, temporary) || withinRoot(temporary, mount.root) {
			return fmt.Errorf("temporary build directory and source overlap for mount %q", mount.name)
		}
	}
	if withinRoot(target, temporary) || withinRoot(temporary, target) {
		return errors.New("output and temporary build directory overlap")
	}
	return nil
}

func acquireBuildWorkspace() (string, func(), error) {
	base := filepath.Join(os.TempDir(), "mdfmt")
	if err := ensurePrivateOutputDirectory(base); err != nil {
		return "", nil, err
	}
	lock := filepath.Join(base, "build.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", nil, fmt.Errorf("another build holds lock %s", lock)
		}
		return "", nil, fmt.Errorf("acquire build lock: %w", err)
	}
	work := filepath.Join(base, "build")
	cleanup := func() {
		_ = os.RemoveAll(work)
		_ = os.Remove(lock)
	}
	if info, err := os.Lstat(work); err == nil && info.Mode()&os.ModeSymlink != 0 {
		cleanup()
		return "", nil, fmt.Errorf("temporary build path is a symlink: %s", work)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		cleanup()
		return "", nil, fmt.Errorf("stat temporary build path: %w", err)
	}
	if err := os.RemoveAll(work); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("clean temporary build path: %w", err)
	}
	if err := os.Mkdir(work, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create temporary build path: %w", err)
	}
	return work, cleanup, nil
}

func publishBuildTarget(target, work string) error {
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("output was replaced by a symlink or non-directory before publication")
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("stat output before publication: %w", statErr)
	}
	entries, err := os.ReadDir(target)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		entries = nil
	default:
		return fmt.Errorf("read output: %w", err)
	}
	markerPath := filepath.Join(target, ".mdfmt")
	if len(entries) > 0 {
		markerInfo, err := os.Lstat(markerPath)
		if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("non-empty output directory is not owned by mdfmt (valid .mdfmt marker required)")
		}
		content, err := os.ReadFile(markerPath)
		if err != nil || string(content) != buildMarker {
			return errors.New("output directory has an invalid .mdfmt marker")
		}
	}
	if err := writeFileAtomic(markerPath, []byte(buildMarker), 0o644); err != nil {
		return fmt.Errorf("write output marker: %w", err)
	}
	entries, err = os.ReadDir(target)
	if err != nil {
		return fmt.Errorf("read output for cleanup: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == ".mdfmt" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(target, entry.Name())); err != nil {
			return fmt.Errorf("remove stale output %s: %w", entry.Name(), err)
		}
	}
	workEntries, err := os.ReadDir(work)
	if err != nil {
		return fmt.Errorf("read completed build: %w", err)
	}
	for _, entry := range workEntries {
		if err := copyBuildTree(filepath.Join(work, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return fmt.Errorf("publish %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func copyBuildTree(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("generated build unexpectedly contains a symlink")
	}
	if info.IsDir() {
		if err := os.Mkdir(target, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyBuildTree(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("generated build contains a non-regular file")
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o644)
}
