package ui

import (
	"fmt"
	"strings"
)

// Styler renders the CLI's visual vocabulary: one glyph per meaning, four
// colours, and no box drawing. The zero value is plain ASCII with no escape
// sequences, which is what tests, pipes and dumb terminals get.
//
// Every method is a pure string transform — nothing here writes to a stream,
// so a command can compose a line and still choose stdout or stderr for it.
type Styler struct {
	color   bool // emit SGR sequences
	unicode bool // use ✓ ✗ → glyphs instead of their ASCII stand-ins
}

// Options describes the one environment a Styler is built for. Env is an
// os.Environ()-shaped slice so tests can state the whole environment instead
// of mutating the process's.
type Options struct {
	NoColorFlag bool     // --no-color
	Env         []string // os.Environ() or a test list
	IsTerminal  bool     // is the destination a TTY
	GOOS        string   // runtime.GOOS
}

// SGR codes. Only the 16-colour palette: the user's terminal theme decides the
// exact shade, which is what keeps the output legible on light and dark
// backgrounds alike.
const (
	sgrReset     = "\x1b[0m"
	sgrBold      = "1"
	sgrDim       = "2"
	sgrUnderline = "4"
	sgrRed       = "31"
	sgrGreen     = "32"
	sgrYellow    = "33"
	sgrCyan      = "36"
)

// The glyph vocabulary. Heavy variants (✔ ✖) render inconsistently across the
// monospace fonts developers actually have, so they are not used; every glyph
// below exists in SF Mono, Menlo, JetBrains Mono, Cascadia, Fira Code and
// DejaVu Sans Mono.
const (
	glyphSuccess = "✓" // U+2713
	glyphFailure = "✗" // U+2717
	glyphWarning = "!"
	glyphNote    = "–" // U+2013
	glyphStep    = "→" // U+2192
	glyphPending = "○" // U+25CB

	asciiSuccess = "ok"
	asciiFailure = "x"
	asciiWarning = "!"
	asciiNote    = "-"
	asciiStep    = ">"
	asciiPending = "o"
)

// DefaultWidth is the column at which prose wraps. Commands and URLs are never
// wrapped, whatever the width.
const DefaultWidth = 80

// New resolves colour and glyph support for one destination.
//
// The order matters and is the one no-color.org asks for: a TTY starts
// coloured, FORCE_COLOR/CLICOLOR_FORCE can turn colour on for a pipe, and
// NO_COLOR (or TERM=dumb, or --no-color) overrides both.
func New(opts Options) Styler {
	env := envLookup(opts.Env)

	color := opts.IsTerminal

	if forced(env, "FORCE_COLOR") || forced(env, "CLICOLOR_FORCE") {
		color = true
	}

	dumb := env("TERM") == "dumb"

	if env("NO_COLOR") != "" || dumb || opts.NoColorFlag {
		color = false
	}

	// Unicode is a property of the destination's encoding, not of its
	// TTY-ness: a UTF-8 log file holds ✓ perfectly well. A dumb terminal is
	// the one place where even the glyph is unsafe.
	unicode := !dumb && utf8Locale(env)

	// The legacy Windows console renders these glyphs as mojibake; Windows
	// Terminal (which sets WT_SESSION) does not.
	if opts.GOOS == "windows" && env("WT_SESSION") == "" {
		unicode = false
	}

	return Styler{color: color, unicode: unicode}
}

// Color reports whether this Styler emits escape sequences.
func (s Styler) Color() bool { return s.color }

// Unicode reports whether this Styler emits the glyphs rather than their ASCII
// stand-ins.
func (s Styler) Unicode() bool { return s.unicode }

// envLookup turns an os.Environ() slice into a lookup function. Later entries
// win, which is what the OS itself does with a duplicated name.
func envLookup(environ []string) func(string) string {
	values := make(map[string]string, len(environ))

	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}

		values[name] = value
	}

	return func(name string) string { return values[name] }
}

// forced reports whether a FORCE_COLOR-style variable asks for colour. Set but
// empty, or set to "0", means "no opinion" — anything else means yes.
func forced(env func(string) string, name string) bool {
	value := env(name)

	return value != "" && value != "0"
}

// utf8Locale reads the first locale variable that is set, in the order the C
// library resolves them, and asks whether it names UTF-8.
func utf8Locale(env func(string) string) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := env(name)
		if value == "" {
			continue
		}

		lower := strings.ToLower(value)

		return strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8")
	}

	return false
}

// paint wraps one run in an SGR sequence. Nothing else in this file emits an
// escape, so a bare reset can never be produced.
func (s Styler) paint(codes, text string) string {
	if !s.color || text == "" {
		return text
	}

	return "\x1b[" + codes + "m" + text + sgrReset
}

func (s Styler) glyph(unicodeGlyph, ascii, codes string) string {
	symbol := ascii
	if s.unicode {
		symbol = unicodeGlyph
	}

	return s.paint(codes, symbol)
}

// Success marks something that worked.
func (s Styler) Success(text string) string {
	return s.glyph(glyphSuccess, asciiSuccess, sgrGreen) + " " + text
}

// Failure marks something the user has to fix.
func (s Styler) Failure(text string) string {
	return s.glyph(glyphFailure, asciiFailure, sgrRed) + " " + text
}

// Warning marks something that is not fatal but wants attention.
func (s Styler) Warning(text string) string {
	return s.glyph(glyphWarning, asciiWarning, sgrYellow) + " " + text
}

// Note marks a neutral aside: skipped, unchanged, informational.
func (s Styler) Note(text string) string {
	return s.glyph(glyphNote, asciiNote, sgrDim) + " " + text
}

// Step marks a step in progress or an action the user is about to take.
func (s Styler) Step(text string) string {
	return s.glyph(glyphStep, asciiStep, sgrCyan) + " " + text
}

// Pending marks a check that has not run.
func (s Styler) Pending(text string) string {
	return s.glyph(glyphPending, asciiPending, sgrDim) + " " + text
}

// Bold is the weight for headings and the final verdict.
func (s Styler) Bold(text string) string { return s.paint(sgrBold, text) }

// Dim is for text that must be present but should not compete.
func (s Styler) Dim(text string) string { return s.paint(sgrDim, text) }

// Accent (cyan) marks anything the user should copy or click.
func (s Styler) Accent(text string) string { return s.paint(sgrCyan, text) }

// Heading opens a section: a blank line, then the title in bold.
func (s Styler) Heading(text string) string { return "\n" + s.Bold(text) }

// Command renders a copyable command on its own line, indented four spaces.
func (s Styler) Command(text string) string { return "    " + s.Accent(text) }

// URL renders a clickable link.
func (s Styler) URL(text string) string { return s.paint(sgrCyan+";"+sgrUnderline, text) }

// Indent shifts every line of text right by n spaces.
func (s Styler) Indent(text string, n int) string {
	if n <= 0 || text == "" {
		return text
	}

	pad := strings.Repeat(" ", n)
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if line == "" {
			continue
		}

		lines[i] = pad + line
	}

	return strings.Join(lines, "\n")
}

// Wrap folds prose at width columns (DefaultWidth when width is not positive).
//
// A line that is a copyable command (four-space indent) or that carries a URL
// is left exactly as it is: a wrapped command cannot be pasted, which defeats
// the point of printing it.
func (s Styler) Wrap(text string, width int) string {
	if width <= 0 {
		width = DefaultWidth
	}

	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))

	for _, line := range lines {
		if strings.HasPrefix(line, "    ") || strings.Contains(line, "://") {
			wrapped = append(wrapped, line)

			continue
		}

		wrapped = append(wrapped, wrapLine(line, width))
	}

	return strings.Join(wrapped, "\n")
}

func wrapLine(line string, width int) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]

	words := strings.Fields(line)
	if len(words) == 0 {
		return line
	}

	var (
		out     strings.Builder
		current = indent + words[0]
	)

	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			out.WriteString(current)
			out.WriteString("\n")

			current = indent + word

			continue
		}

		current += " " + word
	}

	out.WriteString(current)

	return out.String()
}

// Errorf renders a failed run: the cause, and — when there is one — the next
// thing to try.
func (s Styler) Errorf(err error, hint string) string {
	if err == nil {
		return ""
	}

	line := s.paint(sgrRed, "error:") + " " + err.Error()

	if hint == "" {
		return line
	}

	return fmt.Sprintf("%s\n\n  %s", line, s.Step(hint))
}
