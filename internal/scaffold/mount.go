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
		return FileResult{Path: edit.Path, Skipped: true}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return FileResult{}, fmt.Errorf("inspect entry file %s: %w", edit.Path, err)
	}
	if err := atomicWriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return FileResult{}, fmt.Errorf("write entry file %s: %w", edit.Path, err)
	}

	return FileResult{Path: edit.Path}, nil
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
