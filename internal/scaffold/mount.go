package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const webhookMountMarker = "reevit:init webhook"

// FileEdit is a safe, marker-delimited mutation to an existing project file.
type FileEdit struct {
	Path        string
	Description string
	apply       func(string) (string, error)
}

// WebhookMountEdits returns an entry-file edit only when the adapter can
// recognize a standard layout and prove a safe insertion point.
func WebhookMountEdits(project Project) []FileEdit {
	var edit FileEdit
	switch project.Framework {
	case FrameworkExpress:
		edit = expressMountEdit(project)
	case FrameworkFastAPI:
		edit = pythonMountEdit(project, "FastAPI(", "router as reevit_router", "app.include_router(reevit_router)")
	case FrameworkFlask:
		edit = pythonMountEdit(
			project,
			"Flask(",
			"reevit_webhooks",
			"app.register_blueprint(reevit_webhooks)",
		)
	case FrameworkDjango:
		edit = djangoMountEdit(project)
	case FrameworkLaravel:
		edit = laravelMountEdit(project)
	default:
		if project.Stack == StackGo {
			edit = goMountEdit(project)
		}
	}
	if edit.Path == "" {
		return nil
	}

	return []FileEdit{edit}
}

func expressMountEdit(project Project) FileEdit {
	entry := javascriptEntry(project.Root)
	if entry == "" {
		return FileEdit{}
	}
	raw, err := os.ReadFile(filepath.Join(project.Root, entry))
	if err != nil {
		return FileEdit{}
	}
	source := string(raw)
	if strings.HasPrefix(source, "#!") ||
		(!project.TypeScript && !packageIsModule(project.Root)) {
		return FileEdit{}
	}
	appPattern := regexp.MustCompile(`(?m)^([ \t]*)(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*express\(\)\s*;?`)
	match := appPattern.FindStringSubmatchIndex(source)
	if match == nil {
		return FileEdit{}
	}

	return FileEdit{
		Path: entry, Description: "mount the signed Express webhook before JSON parsing",
		apply: func(content string) (string, error) {
			if strings.Contains(content, webhookMountMarker) {
				return content, nil
			}
			match := appPattern.FindStringSubmatchIndex(content)
			if match == nil {
				return "", fmt.Errorf("Express entry no longer has a recognizable app declaration")
			}
			appName := content[match[4]:match[5]]
			relative, err := filepath.Rel(
				filepath.Dir(entry),
				"reevit/webhook",
			)
			if err != nil {
				return "", err
			}
			relative = filepath.ToSlash(relative)
			if !strings.HasPrefix(relative, ".") {
				relative = "./" + relative
			}
			importBlock := fmt.Sprintf(
				"// %s import\nimport { mountReevitWebhook } from %q;\n",
				webhookMountMarker,
				relative,
			)
			content = importBlock + content
			match = appPattern.FindStringSubmatchIndex(content)
			insertion := match[1]
			call := fmt.Sprintf(
				"\n// %s mount\nmountReevitWebhook(%s);\n",
				webhookMountMarker,
				appName,
			)
			jsonPattern := regexp.MustCompile(
				`(?m)^[ \t]*` + regexp.QuoteMeta(appName) +
					`\.use\(\s*express\.json\(`,
			)
			if location := jsonPattern.FindStringIndex(content); location != nil {
				insertion = location[0]
			}

			return content[:insertion] + call + content[insertion:], nil
		},
	}
}

func goMountEdit(project Project) FileEdit {
	entry := firstExisting(project.Root, "main.go")
	if entry == "" {
		return FileEdit{}
	}
	raw, err := os.ReadFile(filepath.Join(project.Root, entry))
	if err != nil {
		return FileEdit{}
	}
	listenPattern := regexp.MustCompile(`(?m)^([ \t]*)http\.ListenAndServe\(([^,\n]+),\s*nil\)`)
	if !listenPattern.Match(raw) {
		return FileEdit{}
	}

	return FileEdit{
		Path: entry, Description: "register /webhooks/reevit on the default Go HTTP mux",
		apply: func(content string) (string, error) {
			if strings.Contains(content, webhookMountMarker) {
				return content, nil
			}
			location := listenPattern.FindStringSubmatchIndex(content)
			if location == nil {
				return "", fmt.Errorf("Go entry no longer uses the default HTTP mux")
			}
			indent := content[location[2]:location[3]]
			block := indent + "// " + webhookMountMarker + "\n" +
				indent + `http.HandleFunc("/webhooks/reevit", HandleReevitWebhook)` + "\n"

			return content[:location[0]] + block + content[location[0]:], nil
		},
	}
}

func pythonMountEdit(
	project Project,
	constructor, importName, mountCall string,
) FileEdit {
	entry := pythonEntry(project.Root, constructor)
	if entry == "" {
		return FileEdit{}
	}
	appPattern := regexp.MustCompile(`(?m)^([ \t]*)app\s*=\s*[^#\n]*` + regexp.QuoteMeta(constructor) + `[^\n]*$`)

	return FileEdit{
		Path: entry, Description: "mount the signed " + string(project.Framework) + " webhook route",
		apply: func(content string) (string, error) {
			if strings.Contains(content, webhookMountMarker) {
				return content, nil
			}
			location := appPattern.FindStringSubmatchIndex(content)
			if location == nil {
				return "", fmt.Errorf("%s entry no longer has a recognizable app", project.Framework)
			}
			indent := content[location[2]:location[3]]
			importLine := "from reevit_webhook import " + importName
			block := "\n" + indent + "# " + webhookMountMarker + "\n" +
				indent + mountCall

			content, err := insertPythonImport(content, importLine)
			if err != nil {
				return "", err
			}
			location = appPattern.FindStringSubmatchIndex(content)
			if location == nil {
				return "", fmt.Errorf("%s entry changed while inserting import", project.Framework)
			}

			return content[:location[1]] + block + content[location[1]:], nil
		},
	}
}

func djangoMountEdit(project Project) FileEdit {
	matches, _ := filepath.Glob(filepath.Join(project.Root, "*", "urls.py"))
	if len(matches) != 1 {
		return FileEdit{}
	}
	entry, err := filepath.Rel(project.Root, matches[0])
	if err != nil {
		return FileEdit{}
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil || !strings.Contains(string(raw), "urlpatterns") ||
		!strings.Contains(string(raw), "path") {
		return FileEdit{}
	}
	listPattern := regexp.MustCompile(`(?m)^([ \t]*)urlpatterns\s*=\s*\[`)

	return FileEdit{
		Path:        filepath.ToSlash(entry),
		Description: "add the signed Reevit webhook to Django urlpatterns",
		apply: func(content string) (string, error) {
			if strings.Contains(content, webhookMountMarker) {
				return content, nil
			}
			location := listPattern.FindStringSubmatchIndex(content)
			if location == nil {
				return "", fmt.Errorf("Django urlpatterns is no longer a recognizable list")
			}
			importLine := "from reevit_webhook import reevit_webhook"
			block := "\n    # " + webhookMountMarker +
				"\n    path(\"webhooks/reevit\", reevit_webhook),"

			content, err := insertPythonImport(content, importLine)
			if err != nil {
				return "", err
			}
			location = listPattern.FindStringSubmatchIndex(content)
			if location == nil {
				return "", fmt.Errorf("Django urlpatterns changed while inserting import")
			}

			return content[:location[1]] + block + content[location[1]:], nil
		},
	}
}

func insertPythonImport(content, importLine string) (string, error) {
	lines := strings.SplitAfter(content, "\n")
	index := 0

	if index < len(lines) && strings.HasPrefix(lines[index], "#!") {
		index++
	}
	for index < len(lines) {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			index++

			continue
		}

		break
	}

	if index < len(lines) {
		trimmed := strings.TrimSpace(lines[index])
		stringStart := strings.TrimLeft(trimmed, "rRuUbBfF")
		quote := ""
		for _, candidate := range []string{`"""`, `'''`} {
			if strings.HasPrefix(stringStart, candidate) {
				quote = candidate

				break
			}
		}
		if quote != "" {
			if strings.Count(trimmed, quote) >= 2 {
				index++
			} else {
				index++
				for index < len(lines) &&
					!strings.Contains(lines[index], quote) {
					index++
				}
				if index == len(lines) {
					return "", fmt.Errorf("unterminated Python module docstring")
				}
				index++
			}
		}
	}

	for index < len(lines) {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			index++

			continue
		}
		if !strings.HasPrefix(trimmed, "from __future__ import ") {
			break
		}
		parentheses := strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
		continued := strings.HasSuffix(trimmed, "\\")
		index++
		for index < len(lines) && (parentheses > 0 || continued) {
			trimmed = strings.TrimSpace(lines[index])
			parentheses += strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
			continued = strings.HasSuffix(trimmed, "\\")
			index++
		}
	}

	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = importLine + "\n"

	return strings.Join(lines, ""), nil
}

func laravelMountEdit(project Project) FileEdit {
	const entry = "bootstrap/app.php"
	raw, err := os.ReadFile(filepath.Join(project.Root, entry))
	if err != nil {
		return FileEdit{}
	}
	routingPattern := regexp.MustCompile(`(?s)(->withRouting\()(.*?)(\n[ \t]*\))`)
	if !routingPattern.Match(raw) || strings.Contains(string(raw), "then:") {
		return FileEdit{}
	}

	return FileEdit{
		Path:        entry,
		Description: "load the signed Reevit route from Laravel bootstrap routing",
		apply: func(content string) (string, error) {
			if strings.Contains(content, webhookMountMarker) {
				return content, nil
			}
			location := routingPattern.FindStringSubmatchIndex(content)
			if location == nil {
				return "", fmt.Errorf("Laravel bootstrap routing is no longer recognizable")
			}
			importLine := "use Illuminate\\Support\\Facades\\Route;\n"
			if strings.Contains(content, importLine) {
				importLine = ""
			}
			insertion := location[5]
			block := "\n        // " + webhookMountMarker + "\n" +
				"        then: function () {\n" +
				"            Route::middleware('api')->group(base_path('routes/reevit.php'));\n" +
				"        },"

			updated := content[:insertion] + block + content[insertion:]
			if importLine != "" {
				phpEnd := strings.Index(updated, "\n")
				if phpEnd < 0 {
					return "", fmt.Errorf("Laravel bootstrap file has no PHP header")
				}
				updated = updated[:phpEnd+1] + "\n" + importLine + updated[phpEnd+1:]
			}

			return updated, nil
		},
	}
}

func javascriptEntry(root string) string {
	if pkg := readPackageJSON(root); pkg != nil {
		if declared, ok := pkg["main"].(string); ok {
			declared = filepath.Clean(strings.TrimSpace(declared))
			if declared != "." && !strings.HasPrefix(declared, "..") &&
				exists(root, declared) {
				return filepath.ToSlash(declared)
			}
		}
	}
	candidates := []string{
		"src/server.ts", "src/index.ts", "server.ts", "index.ts",
		"src/server.js", "src/index.js", "server.js", "index.js",
	}
	return firstExisting(root, candidates...)
}

func pythonEntry(root, constructor string) string {
	for _, candidate := range []string{"main.py", "app.py", "server.py"} {
		raw, err := os.ReadFile(filepath.Join(root, candidate))
		if err == nil && strings.Contains(string(raw), constructor) {
			return candidate
		}
	}
	return ""
}

func packageIsModule(root string) bool {
	pkg := readPackageJSON(root)
	value, _ := pkg["type"].(string)
	return value == "module"
}

func firstExisting(root string, candidates ...string) string {
	for _, candidate := range candidates {
		if exists(root, candidate) {
			return filepath.ToSlash(candidate)
		}
	}
	return ""
}

func validateFileEdit(project Project, edit FileEdit) error {
	raw, err := os.ReadFile(filepath.Join(project.Root, edit.Path))
	if err != nil {
		return fmt.Errorf("read entry file %s: %w", edit.Path, err)
	}
	_, err = edit.apply(string(raw))
	if err != nil {
		return fmt.Errorf("plan entry edit %s: %w", edit.Path, err)
	}
	return nil
}

func applyFileEdit(project Project, edit FileEdit) (FileResult, error) {
	path := filepath.Join(project.Root, edit.Path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return FileResult{}, fmt.Errorf("read entry file %s: %w", edit.Path, err)
	}
	updated, err := edit.apply(string(raw))
	if err != nil {
		return FileResult{}, fmt.Errorf("edit entry file %s: %w", edit.Path, err)
	}
	if updated == string(raw) {
		// A skipped edit must not replace an exact ownership record from the
		// manifest with a pattern-derived legacy record. Legacy discovery is
		// performed by PrepareApply only when no ownership record exists.
		return FileResult{Path: edit.Path, Skipped: true, ManagedEdit: true}, nil
	}
	fragments, err := insertedFragments(string(raw), updated)
	if err != nil {
		return FileResult{}, fmt.Errorf("record entry edit %s: %w", edit.Path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return FileResult{}, fmt.Errorf("inspect entry file %s: %w", edit.Path, err)
	}
	if err := atomicWriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return FileResult{}, fmt.Errorf("write entry file %s: %w", edit.Path, err)
	}

	return FileResult{
		Path: edit.Path, ManagedEdit: true,
		Edit: &GeneratedEdit{
			Path:      filepath.ToSlash(filepath.Clean(edit.Path)),
			Kind:      webhookMountMarker,
			Fragments: fragments,
		},
	}, nil
}

func insertedFragments(before, after string) ([]string, error) {
	beforeLines := strings.SplitAfter(before, "\n")
	afterLines := strings.SplitAfter(after, "\n")
	var fragments []string
	afterIndex := 0
	for _, originalLine := range beforeLines {
		match := afterIndex
		for match < len(afterLines) && afterLines[match] != originalLine {
			match++
		}
		if match == len(afterLines) {
			return insertedByteFragments(before, after)
		}
		if match > afterIndex {
			fragments = append(fragments, strings.Join(afterLines[afterIndex:match], ""))
		}
		afterIndex = match + 1
	}
	if afterIndex < len(afterLines) {
		fragments = append(fragments, strings.Join(afterLines[afterIndex:], ""))
	}
	if len(fragments) == 0 {
		return nil, fmt.Errorf("entry edit produced no trackable insertion")
	}
	return fragments, nil
}

func insertedByteFragments(before, after string) ([]string, error) {
	fragments := []string{}
	afterIndex := 0
	for beforeIndex := 0; beforeIndex < len(before); beforeIndex++ {
		match := strings.IndexByte(after[afterIndex:], before[beforeIndex])
		if match < 0 {
			return nil, fmt.Errorf("entry edit replaced existing content")
		}
		match += afterIndex
		if match > afterIndex {
			fragments = append(fragments, after[afterIndex:match])
		}
		afterIndex = match + 1
	}
	if afterIndex < len(after) {
		fragments = append(fragments, after[afterIndex:])
	}
	if len(fragments) == 0 {
		return nil, fmt.Errorf("entry edit produced no trackable insertion")
	}
	return fragments, nil
}

func DiscoverLegacyWebhookEdits(project Project) ([]GeneratedEdit, error) {
	candidates := []string{
		"src/server.ts", "src/index.ts", "server.ts", "index.ts",
		"src/server.js", "src/index.js", "server.js", "index.js",
		"main.go", "main.py", "app.py", "server.py", "bootstrap/app.php",
	}
	if declared := javascriptEntry(project.Root); declared != "" {
		candidates = append(candidates, declared)
	}
	if matches, _ := filepath.Glob(filepath.Join(project.Root, "*", "urls.py")); len(matches) > 0 {
		for _, match := range matches {
			if relative, err := filepath.Rel(project.Root, match); err == nil {
				candidates = append(candidates, relative)
			}
		}
	}

	seen := map[string]bool{}
	var records []GeneratedEdit
	for _, candidate := range candidates {
		candidate = filepath.ToSlash(filepath.Clean(candidate))
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		raw, err := os.ReadFile(filepath.Join(project.Root, candidate))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect legacy webhook mount %s: %w", candidate, err)
		}
		if !strings.Contains(string(raw), webhookMountMarker) {
			continue
		}
		record, err := legacyGeneratedEdit(candidate, string(raw))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func legacyGeneratedEdit(path, content string) (GeneratedEdit, error) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^// ` + regexp.QuoteMeta(webhookMountMarker) + ` import\nimport \{ mountReevitWebhook \} from "[^"\n]+";\n`),
		regexp.MustCompile(`(?m)^\n?// ` + regexp.QuoteMeta(webhookMountMarker) + ` mount\nmountReevitWebhook\([A-Za-z_$][A-Za-z0-9_$]*\);\n`),
		regexp.MustCompile(`(?m)^[ \t]*// ` + regexp.QuoteMeta(webhookMountMarker) + `\n[ \t]*http\.HandleFunc\("/webhooks/reevit", HandleReevitWebhook\)\n`),
		regexp.MustCompile(`(?m)^from reevit_webhook import (?:router as reevit_router|reevit_webhooks|reevit_webhook)\n`),
		regexp.MustCompile(`(?m)^\n?[ \t]*# ` + regexp.QuoteMeta(webhookMountMarker) + `\n[ \t]*(?:app\.include_router\(reevit_router\)|app\.register_blueprint\(reevit_webhooks\))`),
		regexp.MustCompile(`(?m)^\n?[ \t]*# ` + regexp.QuoteMeta(webhookMountMarker) + `\n[ \t]*path\("webhooks/reevit", reevit_webhook\),`),
		regexp.MustCompile(`(?m)^[ \t]*// ` + regexp.QuoteMeta(webhookMountMarker) + `\n[ \t]*then: function \(\) \{\n[ \t]*Route::middleware\('api'\)->group\(base_path\('routes/reevit\.php'\)\);\n[ \t]*\},`),
	}
	var fragments []string
	capturedMarkers := 0
	for _, pattern := range patterns {
		match := pattern.FindString(content)
		if match == "" {
			continue
		}
		fragments = append(fragments, match)
		capturedMarkers += strings.Count(match, webhookMountMarker)
	}
	if markerCount := strings.Count(content, webhookMountMarker); markerCount == 0 ||
		capturedMarkers != markerCount {
		return GeneratedEdit{}, fmt.Errorf(
			"managed webhook mount in %s changed after setup; restore it from backup or remove it manually",
			path,
		)
	}
	return GeneratedEdit{
		Path:      filepath.ToSlash(filepath.Clean(path)),
		Kind:      webhookMountMarker + ":legacy",
		Fragments: fragments,
	}, nil
}

func removeGeneratedEdit(project Project, edit GeneratedEdit) (FileResult, error) {
	path := filepath.Join(project.Root, edit.Path)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return FileResult{
			Path: edit.Path, Removed: true, ManagedEdit: true, Edit: &edit,
		}, nil
	}
	if err != nil {
		return FileResult{}, fmt.Errorf("read entry file %s: %w", edit.Path, err)
	}
	if len(edit.Fragments) == 0 {
		return FileResult{}, fmt.Errorf(
			"entry edit %s has no exact ownership record; restore it from backup or remove it manually",
			edit.Path,
		)
	}
	updated := string(raw)
	present := 0
	for _, fragment := range edit.Fragments {
		count := strings.Count(updated, fragment)
		if fragment == "" || count > 1 {
			return FileResult{}, fmt.Errorf(
				"managed edit in %s changed after setup; no mount code was removed — review it and rerun init",
				edit.Path,
			)
		}
		if count == 1 {
			present++
		}
	}
	if present == 0 {
		if strings.Contains(updated, webhookMountMarker) {
			return FileResult{}, fmt.Errorf(
				"managed edit in %s changed after setup; no mount code was removed — review it and rerun init",
				edit.Path,
			)
		}
		return FileResult{
			Path: edit.Path, Removed: true, ManagedEdit: true, Edit: &edit,
		}, nil
	}
	if present != len(edit.Fragments) {
		return FileResult{}, fmt.Errorf(
			"managed edit in %s changed after setup; no mount code was removed — review it and rerun init",
			edit.Path,
		)
	}
	for _, fragment := range edit.Fragments {
		updated = strings.Replace(updated, fragment, "", 1)
	}
	info, err := os.Stat(path)
	if err != nil {
		return FileResult{}, fmt.Errorf("inspect entry file %s: %w", edit.Path, err)
	}
	if err := atomicWriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return FileResult{}, fmt.Errorf("remove entry edit %s: %w", edit.Path, err)
	}
	return FileResult{
		Path: edit.Path, Removed: true, ManagedEdit: true, Edit: &edit,
	}, nil
}

func atomicWriteFile(path string, content []byte, mode os.FileMode) (resultErr error) {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".reevit-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if resultErr != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(content); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}

	return os.Rename(tempPath, path)
}
