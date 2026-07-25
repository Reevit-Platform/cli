package scaffold

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const checkoutPlacementMarker = "reevit-checkout"

// CheckoutField is information the generated checkout form collects before
// opening the provider UI.
type CheckoutField string

const (
	CheckoutFieldAmount    CheckoutField = "amount"
	CheckoutFieldName      CheckoutField = "name"
	CheckoutFieldEmail     CheckoutField = "email"
	CheckoutFieldPhone     CheckoutField = "phone"
	CheckoutFieldReference CheckoutField = "reference"
)

// CheckoutOptions controls the generated form and optional insertion into an
// existing application page.
type CheckoutOptions struct {
	PagePath       string
	Fields         []CheckoutField
	MetadataFields []string
}

var (
	metadataFieldPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	jsIdentifierPattern   = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*`)
	vueScriptSetupPattern = regexp.MustCompile(`(?i)<script\b[^>]*\bsetup\b[^>]*>`)
)

// ParseCheckoutFields validates the public CLI field names.
func ParseCheckoutFields(values []string) ([]CheckoutField, error) {
	allowed := map[CheckoutField]bool{
		CheckoutFieldAmount: true, CheckoutFieldName: true, CheckoutFieldEmail: true,
		CheckoutFieldPhone: true, CheckoutFieldReference: true,
	}
	seen := map[CheckoutField]bool{}
	fields := make([]CheckoutField, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "price" {
			normalized = string(CheckoutFieldAmount)
		}
		field := CheckoutField(normalized)
		if field == "" {
			continue
		}
		if !allowed[field] {
			return nil, fmt.Errorf("unknown checkout field %q — available: amount (or price), name, email, phone, reference", value)
		}
		if !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return fields, nil
}

// ParseMetadataFields returns safe, deduplicated metadata keys. These keys are
// attached unchanged and become metadata_<key> variables in workflows.
func ParseMetadataFields(values []string) ([]string, error) {
	seen := map[string]bool{}
	fields := make([]string, 0, len(values))
	for _, value := range values {
		field := strings.TrimSpace(value)
		if field == "" {
			continue
		}
		if !metadataFieldPattern.MatchString(field) {
			return nil, fmt.Errorf("invalid metadata field %q — use letters, numbers, and underscores, starting with a letter or underscore", field)
		}
		if !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return fields, nil
}

// CheckoutPageCandidates finds likely renderable pages, preferring the
// framework's root entry page.
func CheckoutPageCandidates(project Project) []string {
	extensions := map[string]bool{}
	switch project.Stack {
	case StackNext, StackReact:
		extensions[".tsx"], extensions[".jsx"] = true, true
	case StackVue:
		extensions[".vue"] = true
	case StackSvelte:
		extensions[".svelte"] = true
	default:
		return nil
	}

	var candidates []string
	_ = filepath.WalkDir(project.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != project.Root {
				switch entry.Name() {
				case "node_modules", ".git", ".next", ".nuxt", ".svelte-kit", "dist", "build", "coverage":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !extensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, relErr := filepath.Rel(project.Root, path)
		if relErr == nil {
			candidates = append(candidates, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := checkoutPageScore(project, candidates[i]), checkoutPageScore(project, candidates[j])
		if left != right {
			return left < right
		}
		return candidates[i] < candidates[j]
	})
	return candidates
}

func checkoutPageScore(project Project, path string) int {
	lower := strings.ToLower(path)
	if project.Stack == StackNext {
		for i, preferred := range []string{"app/page.tsx", "src/app/page.tsx", "app/page.jsx", "src/app/page.jsx"} {
			if lower == preferred {
				return i
			}
		}
		if strings.Contains(lower, "/page.") {
			return 10
		}
	}
	if project.Framework == FrameworkSvelteKit && strings.HasSuffix(lower, "/+page.svelte") {
		return 0
	}
	if strings.HasSuffix(lower, "/app.tsx") || strings.HasSuffix(lower, "/app.jsx") ||
		strings.HasSuffix(lower, "/app.vue") || strings.HasSuffix(lower, "/app.svelte") {
		return 0
	}
	if strings.Contains(lower, "/pages/") || strings.Contains(lower, "/routes/") {
		return 10
	}
	if strings.Contains(lower, "/components/") || strings.Contains(lower, "reevit-demo") {
		return 100
	}
	return 50
}

// ConfigureCheckoutTarget validates checkout configuration and adds a managed
// page edit without disturbing the target's generated demo files.
func ConfigureCheckoutTarget(project Project, target *Target) error {
	if target == nil || target.Checkout == nil {
		return nil
	}
	if _, err := ParseMetadataFields(target.Checkout.MetadataFields); err != nil {
		return err
	}
	page := strings.TrimSpace(target.Checkout.PagePath)
	if page == "" {
		return nil
	}
	if err := validateOutputPath(project.Root, page); err != nil {
		return fmt.Errorf("invalid checkout page: %w", err)
	}
	info, err := os.Stat(filepath.Join(project.Root, filepath.Clean(page)))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("checkout page %q does not exist", page)
		}
		return fmt.Errorf("inspect checkout page: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("checkout page %q is a directory", page)
	}
	component, err := checkoutComponentPath(*target)
	if err != nil {
		return err
	}
	if filepath.Clean(component) == filepath.Clean(page) {
		return fmt.Errorf("checkout page and component must use different paths")
	}
	edit, err := checkoutPageEdit(project, page, component)
	if err != nil {
		return err
	}
	target.Edits = append(target.Edits, edit)
	return nil
}

func checkoutComponentPath(target Target) (string, error) {
	for name, path := range target.Files {
		switch name {
		case "next-checkout.tsx.tmpl", "react-checkout.tsx.tmpl",
			"vue-checkout.vue.tmpl", "svelte-checkout.svelte.tmpl":
			return path, nil
		}
	}
	return "", fmt.Errorf("checkout target has no component output")
}

func checkoutPageEdit(project Project, page, component string) (FileEdit, error) {
	importPath, err := filepath.Rel(filepath.Dir(filepath.Clean(page)), filepath.Clean(component))
	if err != nil {
		return FileEdit{}, fmt.Errorf("resolve checkout import: %w", err)
	}
	importPath = filepath.ToSlash(importPath)
	if !strings.HasPrefix(importPath, ".") {
		importPath = "./" + importPath
	}
	if project.Stack == StackNext || project.Stack == StackReact {
		importPath = strings.TrimSuffix(importPath, filepath.Ext(importPath))
	}
	return FileEdit{
		Path:        filepath.ToSlash(filepath.Clean(page)),
		Kind:        checkoutPlacementMarker,
		Description: "add the configured checkout to an existing page",
		apply: func(content string) (string, error) {
			if strings.Contains(content, checkoutPlacementMarker+":start") {
				return content, nil
			}
			switch project.Stack {
			case StackNext, StackReact:
				return injectReactCheckout(content, importPath)
			case StackVue:
				return injectVueCheckout(content, importPath)
			case StackSvelte:
				return injectSvelteCheckout(content, importPath)
			default:
				return "", fmt.Errorf("checkout page insertion is not supported for %s", project.Stack)
			}
		},
	}, nil
}

func checkoutConfigID(options *CheckoutOptions) string {
	if options == nil {
		return "default"
	}
	config := fmt.Sprintf("%v|%v", options.Fields, options.MetadataFields)
	hash := sha256.Sum256([]byte(config))
	return fmt.Sprintf("%x", hash[:4])
}

func injectReactCheckout(content, importPath string) (string, error) {
	importLine := fmt.Sprintf("import { ReevitCheckoutButton } from %q; // %s:import\n", importPath, checkoutPlacementMarker)
	insertAt := 0
	trimmed := strings.TrimLeft(content, " \t\r\n")
	leading := len(content) - len(trimmed)
	for _, directive := range []string{`"use client";`, `'use client';`} {
		if strings.HasPrefix(trimmed, directive) {
			if newline := strings.Index(content[leading+len(directive):], "\n"); newline >= 0 {
				insertAt = leading + len(directive) + newline + 1
			} else {
				insertAt = len(content)
			}
			break
		}
	}
	content = content[:insertAt] + importLine + content[insertAt:]

	rootStart, closeAt, selfClosing, err := findDefaultExportJSXRoot(content)
	if err != nil {
		return "", err
	}
	if selfClosing {
		lineStart := strings.LastIndex(content[:rootStart], "\n") + 1
		indent := content[lineStart:rootStart]
		if strings.TrimSpace(indent) != "" {
			indent = ""
		}
		root := content[rootStart:closeAt]
		replacement := "<>\n" +
			indent + "  " + root + "\n" +
			indent + "  {/* " + checkoutPlacementMarker + ":start */}\n" +
			indent + "  <ReevitCheckoutButton amount={5000} />\n" +
			indent + "  {/* " + checkoutPlacementMarker + ":end */}\n" +
			indent + "</>"
		return content[:rootStart] + replacement + content[closeAt:], nil
	}
	lineStart := strings.LastIndex(content[:closeAt], "\n") + 1
	indent := content[lineStart:closeAt]
	if strings.TrimSpace(indent) != "" {
		indent = ""
	}
	block := indent + "{/* " + checkoutPlacementMarker + ":start */}\n" +
		indent + "<ReevitCheckoutButton amount={5000} />\n" +
		indent + "{/* " + checkoutPlacementMarker + ":end */}\n"
	return content[:lineStart] + block + content[lineStart:], nil
}

// findDefaultExportJSXRoot constrains insertion to the exported page component
// rather than a helper component later in the same file.
func findDefaultExportJSXRoot(content string) (start, end int, selfClosing bool, err error) {
	exportAt := strings.Index(content, "export default")
	if exportAt < 0 {
		return 0, 0, false, fmt.Errorf("checkout page needs an inline default-exported component")
	}
	searchAt := exportAt + len("export default")
	afterExport := content[searchAt:]
	returnAt := strings.Index(afterExport, "return")
	arrowAt := strings.Index(afterExport, "=>")
	functionComponent := strings.HasPrefix(strings.TrimSpace(afterExport), "function ") ||
		strings.HasPrefix(strings.TrimSpace(afterExport), "async function ")
	trimmedExport := strings.TrimSpace(afterExport)
	exportedName := jsIdentifierPattern.FindString(trimmedExport)
	afterName := strings.TrimSpace(strings.TrimPrefix(trimmedExport, exportedName))
	namedExport := exportedName != "" &&
		exportedName != "function" && exportedName != "async" && exportedName != "class" &&
		(afterName == "" || strings.HasPrefix(afterName, ";"))
	if namedExport || (returnAt < 0 && arrowAt < 0) {
		definitionAt := -1
		definitionPrefix := ""
		for _, prefix := range []string{"function ", "const ", "let ", "var "} {
			if at := strings.LastIndex(content[:exportAt], prefix+exportedName); at > definitionAt {
				definitionAt = at
				definitionPrefix = prefix
			}
		}
		if exportedName == "" || definitionAt < 0 {
			return 0, 0, false, fmt.Errorf("checkout page needs a default-exported function or component")
		}
		searchAt = definitionAt
		afterExport = content[searchAt:exportAt]
		returnAt = strings.Index(afterExport, "return")
		arrowAt = strings.Index(afterExport, "=>")
		functionComponent = definitionPrefix == "function "
	}
	switch {
	case functionComponent && returnAt >= 0:
		scopedReturn := findFunctionReturn(content, searchAt)
		if scopedReturn < 0 {
			return 0, 0, false, fmt.Errorf("could not find the page component return")
		}
		searchAt = scopedReturn + len("return")
	case arrowAt >= 0:
		searchAt += arrowAt + len("=>")
	case returnAt >= 0:
		searchAt += returnAt + len("return")
	default:
		return 0, 0, false, fmt.Errorf("checkout page needs a default-exported function or component")
	}

	tags := scanJSXTags(content, searchAt)
	if len(tags) == 0 {
		return 0, 0, false, fmt.Errorf("could not find JSX in the default-exported checkout page")
	}
	first := tags[0]
	start = first[0]
	firstText := content[first[0]:first[1]]
	if strings.HasSuffix(strings.TrimSpace(firstText), "/>") {
		return start, first[1], true, nil
	}
	firstName := ""
	if firstText != "<>" {
		firstName = jsxTagName(firstText)
	}
	stack := []string{firstName}
	for _, tag := range tags[1:] {
		tagStart := tag[0]
		text := content[tag[0]:tag[1]]
		switch {
		case text == "<>":
			stack = append(stack, "")
		case text == "</>":
			if len(stack) == 0 || stack[len(stack)-1] != "" {
				return 0, 0, false, fmt.Errorf("could not safely match the checkout page JSX fragment")
			}
			stack = stack[:len(stack)-1]
		case strings.HasPrefix(text, "</"):
			name := jsxTagName(text)
			if len(stack) == 0 || stack[len(stack)-1] != name {
				return 0, 0, false, fmt.Errorf("could not safely match the checkout page JSX root")
			}
			stack = stack[:len(stack)-1]
		case strings.HasSuffix(strings.TrimSpace(text), "/>"):
			continue
		default:
			stack = append(stack, jsxTagName(text))
		}
		if len(stack) == 0 {
			return start, tagStart, false, nil
		}
	}
	return 0, 0, false, fmt.Errorf("could not find the closing JSX root in the default-exported checkout page")
}

func findFunctionReturn(content string, functionAt int) int {
	bodyAt := findFunctionBody(content, functionAt)
	if bodyAt < 0 {
		return -1
	}
	depth := 0
	var quote byte
	escaped := false
	lineComment, blockComment := false, false
	for i := bodyAt; i < len(content); i++ {
		ch := content[i]
		next := byte(0)
		if i+1 < len(content) {
			next = content[i+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '/' && next == '/' {
			lineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			continue
		}
		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return -1
			}
			continue
		}
		if depth == 1 && strings.HasPrefix(content[i:], "return") &&
			(i == 0 || !isJSIdentifierByte(content[i-1])) &&
			(i+len("return") == len(content) || !isJSIdentifierByte(content[i+len("return")])) {
			return i
		}
	}
	return -1
}

func findFunctionBody(content string, functionAt int) int {
	paramsAt := strings.Index(content[functionAt:], "(")
	if paramsAt < 0 {
		return -1
	}
	paramsAt += functionAt
	parenDepth := 0
	var quote byte
	escaped := false
	paramsEnd := -1
	for i := paramsAt; i < len(content); i++ {
		ch := content[i]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			continue
		}
		switch ch {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				paramsEnd = i + 1
				i = len(content)
			}
		}
	}
	if paramsEnd < 0 {
		return -1
	}
	i := paramsEnd
	for i < len(content) && strings.ContainsRune(" \t\r\n", rune(content[i])) {
		i++
	}
	if i >= len(content) {
		return -1
	}
	if content[i] != ':' {
		bodyOffset := strings.Index(content[i:], "{")
		if bodyOffset < 0 {
			return -1
		}
		return i + bodyOffset
	}
	i++
	for i < len(content) && strings.ContainsRune(" \t\r\n", rune(content[i])) {
		i++
	}
	if i >= len(content) {
		return -1
	}
	if content[i] != '{' {
		bodyOffset := strings.Index(content[i:], "{")
		if bodyOffset < 0 {
			return -1
		}
		return i + bodyOffset
	}
	typeDepth := 0
	for ; i < len(content); i++ {
		switch content[i] {
		case '{':
			typeDepth++
		case '}':
			typeDepth--
			if typeDepth == 0 {
				bodyOffset := strings.Index(content[i+1:], "{")
				if bodyOffset < 0 {
					return -1
				}
				return i + 1 + bodyOffset
			}
		}
	}
	return -1
}

func isJSIdentifierByte(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') || ch == '_' || ch == '$'
}

func scanJSXTags(content string, start int) [][2]int {
	var tags [][2]int
	braceDepth := 0
	var quote byte
	escaped := false
	for i := start; i < len(content); i++ {
		ch := content[i]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		if braceDepth > 0 && (ch == '"' || ch == '\'' || ch == '`') {
			quote = ch
			continue
		}
		if ch == '{' {
			braceDepth++
			continue
		}
		if ch == '}' && braceDepth > 0 {
			braceDepth--
			continue
		}
		if ch != '<' || braceDepth != 0 || i+1 >= len(content) {
			continue
		}
		next := content[i+1]
		if !(next == '>' || next == '/' || (next >= 'A' && next <= 'Z') || (next >= 'a' && next <= 'z')) {
			continue
		}
		tagBraceDepth := 0
		var tagQuote byte
		tagEscaped := false
		for j := i + 1; j < len(content); j++ {
			tagCh := content[j]
			if tagQuote != 0 {
				if tagEscaped {
					tagEscaped = false
				} else if tagCh == '\\' {
					tagEscaped = true
				} else if tagCh == tagQuote {
					tagQuote = 0
				}
				continue
			}
			if tagCh == '"' || tagCh == '\'' || tagCh == '`' {
				tagQuote = tagCh
				continue
			}
			if tagCh == '{' {
				tagBraceDepth++
				continue
			}
			if tagCh == '}' && tagBraceDepth > 0 {
				tagBraceDepth--
				continue
			}
			if tagCh == '>' && tagBraceDepth == 0 {
				tags = append(tags, [2]int{i, j + 1})
				i = j
				break
			}
		}
	}
	return tags
}

func jsxTagName(tag string) string {
	return strings.Trim(strings.Fields(strings.Trim(tag, "<>/ \t\r\n"))[0], "/")
}

func injectVueCheckout(content, importPath string) (string, error) {
	importLine := fmt.Sprintf("import ReevitCheckoutButton from %q; // %s:import\n", importPath, checkoutPlacementMarker)
	setupMatch := vueScriptSetupPattern.FindStringIndex(content)
	setupAt := -1
	if setupMatch != nil {
		setupAt = setupMatch[0]
	}
	if setupAt < 0 {
		content = "<script setup>\n" + importLine + "</script>\n\n" + content
	} else {
		scriptEnd := strings.Index(content[setupAt:], "</script>")
		if scriptEnd < 0 {
			return "", fmt.Errorf("could not find </script> for Vue script setup")
		}
		scriptEnd += setupAt
		if scriptEnd > 0 && content[scriptEnd-1] != '\n' {
			importLine = "\n" + importLine
		}
		content = content[:scriptEnd] + importLine + content[scriptEnd:]
	}
	templateEnd := strings.LastIndex(content, "</template>")
	if templateEnd < 0 {
		return "", fmt.Errorf("could not find </template> in checkout page")
	}
	block := "  <!-- " + checkoutPlacementMarker + ":start -->\n" +
		"  <ReevitCheckoutButton :amount=\"5000\" />\n" +
		"  <!-- " + checkoutPlacementMarker + ":end -->\n"
	return content[:templateEnd] + block + content[templateEnd:], nil
}

func injectSvelteCheckout(content, importPath string) (string, error) {
	importLine := fmt.Sprintf("  import ReevitCheckoutButton from %q; // %s:import\n", importPath, checkoutPlacementMarker)
	scriptEnd := strings.Index(content, "</script>")
	if scriptEnd < 0 {
		content = "<script>\n" + importLine + "</script>\n\n" + content
	} else {
		if scriptEnd > 0 && content[scriptEnd-1] != '\n' {
			importLine = "\n" + importLine
		}
		content = content[:scriptEnd] + importLine + content[scriptEnd:]
	}
	block := "\n<!-- " + checkoutPlacementMarker + ":start -->\n" +
		"<ReevitCheckoutButton amount={5000} />\n" +
		"<!-- " + checkoutPlacementMarker + ":end -->\n"
	return content + block, nil
}
