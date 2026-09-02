package ui

import (
	"errors"
	"strings"
	"testing"
)

// colorEnv is a UTF-8 locale with nothing forcing or forbidding colour, so a
// case's own variables are the only thing deciding the outcome.
var utf8Env = []string{"LANG=en_US.UTF-8"}

func TestNewResolvesColor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts Options
		want bool
	}{
		"a TTY is coloured": {
			opts: Options{IsTerminal: true, Env: utf8Env},
			want: true,
		},
		"a pipe is not": {
			opts: Options{IsTerminal: false, Env: utf8Env},
			want: false,
		},
		"FORCE_COLOR turns a pipe on": {
			opts: Options{Env: []string{"FORCE_COLOR=1"}},
			want: true,
		},
		"CLICOLOR_FORCE turns a pipe on": {
			opts: Options{Env: []string{"CLICOLOR_FORCE=1"}},
			want: true,
		},
		"FORCE_COLOR=0 is not a request": {
			opts: Options{Env: []string{"FORCE_COLOR=0"}},
			want: false,
		},
		"FORCE_COLOR set but empty is not a request": {
			opts: Options{Env: []string{"FORCE_COLOR="}},
			want: false,
		},
		"NO_COLOR beats FORCE_COLOR": {
			opts: Options{Env: []string{"FORCE_COLOR=1", "NO_COLOR=1"}},
			want: false,
		},
		"NO_COLOR beats a TTY": {
			opts: Options{IsTerminal: true, Env: []string{"NO_COLOR=1"}},
			want: false,
		},
		"NO_COLOR set but empty is not set": {
			opts: Options{IsTerminal: true, Env: []string{"NO_COLOR="}},
			want: true,
		},
		"TERM=dumb beats a TTY": {
			opts: Options{IsTerminal: true, Env: []string{"TERM=dumb"}},
			want: false,
		},
		"TERM=dumb beats FORCE_COLOR": {
			opts: Options{Env: []string{"TERM=dumb", "FORCE_COLOR=1"}},
			want: false,
		},
		"--no-color beats a TTY": {
			opts: Options{IsTerminal: true, NoColorFlag: true, Env: utf8Env},
			want: false,
		},
		"--no-color beats FORCE_COLOR": {
			opts: Options{NoColorFlag: true, Env: []string{"FORCE_COLOR=1"}},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := New(tc.opts).Color(); got != tc.want {
				t.Fatalf("Color() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewResolvesUnicode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts Options
		want bool
	}{
		"a UTF-8 LANG is enough": {
			opts: Options{Env: []string{"LANG=en_US.UTF-8"}},
			want: true,
		},
		"lowercase utf8 counts": {
			opts: Options{Env: []string{"LANG=en_GB.utf8"}},
			want: true,
		},
		"LC_ALL wins over LANG": {
			opts: Options{Env: []string{"LC_ALL=C", "LANG=en_US.UTF-8"}},
			want: false,
		},
		"LC_CTYPE wins over LANG": {
			opts: Options{Env: []string{"LC_CTYPE=UTF-8", "LANG=C"}},
			want: true,
		},
		"an empty LC_ALL falls through to LANG": {
			opts: Options{Env: []string{"LC_ALL=", "LANG=en_US.UTF-8"}},
			want: true,
		},
		"no locale at all is ASCII": {
			opts: Options{Env: nil},
			want: false,
		},
		"a POSIX locale is ASCII": {
			opts: Options{Env: []string{"LANG=POSIX"}},
			want: false,
		},
		"a UTF-8 pipe still gets glyphs": {
			opts: Options{IsTerminal: false, Env: utf8Env},
			want: true,
		},
		"TERM=dumb is ASCII whatever the locale": {
			opts: Options{IsTerminal: true, Env: []string{"TERM=dumb", "LANG=en_US.UTF-8"}},
			want: false,
		},
		"Windows Terminal gets glyphs": {
			opts: Options{GOOS: "windows", Env: []string{"LANG=en_US.UTF-8", "WT_SESSION=abc"}},
			want: true,
		},
		"the legacy Windows console does not": {
			opts: Options{GOOS: "windows", Env: utf8Env},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := New(tc.opts).Unicode(); got != tc.want {
				t.Fatalf("Unicode() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGlyphs pins the exact bytes of every glyph in both modes. These strings
// are the CLI's visual contract; a change here is a change every command's
// output inherits.
func TestGlyphs(t *testing.T) {
	t.Parallel()

	rich := New(Options{IsTerminal: true, Env: utf8Env})
	plain := Styler{}

	tests := []struct {
		name      string
		render    func(Styler) string
		wantRich  string
		wantPlain string
	}{
		{"success", func(s Styler) string { return s.Success("ok") }, "\x1b[32m✓\x1b[0m ok", "ok ok"},
		{"failure", func(s Styler) string { return s.Failure("bad") }, "\x1b[31m✗\x1b[0m bad", "x bad"},
		{"warning", func(s Styler) string { return s.Warning("hm") }, "\x1b[33m!\x1b[0m hm", "! hm"},
		{"note", func(s Styler) string { return s.Note("fyi") }, "\x1b[2m–\x1b[0m fyi", "- fyi"},
		{"step", func(s Styler) string { return s.Step("go") }, "\x1b[36m→\x1b[0m go", "> go"},
		{"pending", func(s Styler) string { return s.Pending("later") }, "\x1b[2m○\x1b[0m later", "o later"},
		{"bold", func(s Styler) string { return s.Bold("t") }, "\x1b[1mt\x1b[0m", "t"},
		{"dim", func(s Styler) string { return s.Dim("t") }, "\x1b[2mt\x1b[0m", "t"},
		{"accent", func(s Styler) string { return s.Accent("t") }, "\x1b[36mt\x1b[0m", "t"},
		{"heading", func(s Styler) string { return s.Heading("Project") }, "\n\x1b[1mProject\x1b[0m", "\nProject"},
		{
			"command",
			func(s Styler) string { return s.Command("reevit doctor") },
			"    \x1b[36mreevit doctor\x1b[0m",
			"    reevit doctor",
		},
		{
			"url",
			func(s Styler) string { return s.URL("https://reevit.io") },
			"\x1b[36;4mhttps://reevit.io\x1b[0m",
			"https://reevit.io",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.render(rich); got != tc.wantRich {
				t.Errorf("coloured = %q, want %q", got, tc.wantRich)
			}

			if got := tc.render(plain); got != tc.wantPlain {
				t.Errorf("plain = %q, want %q", got, tc.wantPlain)
			}
		})
	}
}

// TestGlyphsAreNeverColourOnly — every coloured element carries a glyph or a
// word too, so NO_COLOR output still distinguishes the six meanings.
func TestGlyphsAreNeverColourOnly(t *testing.T) {
	t.Parallel()

	ascii := Styler{}
	unicodeOnly := New(Options{Env: utf8Env})

	for _, sty := range []Styler{ascii, unicodeOnly} {
		seen := map[string]string{}

		for name, rendered := range map[string]string{
			"success": sty.Success("x"),
			"failure": sty.Failure("x"),
			"warning": sty.Warning("x"),
			"note":    sty.Note("x"),
			"step":    sty.Step("x"),
			"pending": sty.Pending("x"),
		} {
			marker, _, _ := strings.Cut(rendered, " ")
			if other, clash := seen[marker]; clash {
				t.Errorf("%s and %s both render as %q", name, other, marker)
			}

			seen[marker] = name
		}
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()

	t.Run("prose folds at the width", func(t *testing.T) {
		t.Parallel()

		got := (Styler{}).Wrap("aaaa bbbb cccc dddd", 10)
		if got != "aaaa bbbb\ncccc dddd" {
			t.Fatalf("wrapped = %q", got)
		}
	})

	t.Run("a command line is never folded", func(t *testing.T) {
		t.Parallel()

		command := "    reevit listen --forward-to http://localhost:3000/api/webhooks/reevit"

		if got := (Styler{}).Wrap(command, 40); got != command {
			t.Fatalf("wrapped = %q, want it untouched", got)
		}
	})

	t.Run("a URL is never folded", func(t *testing.T) {
		t.Parallel()

		line := "open https://dashboard.reevit.io/developers/api-keys to create one"

		if got := (Styler{}).Wrap(line, 20); got != line {
			t.Fatalf("wrapped = %q, want it untouched", got)
		}
	})

	t.Run("indentation survives", func(t *testing.T) {
		t.Parallel()

		got := (Styler{}).Wrap("  aaaa bbbb cccc", 8)
		if got != "  aaaa\n  bbbb\n  cccc" {
			t.Fatalf("wrapped = %q", got)
		}
	})

	t.Run("a non-positive width means 80", func(t *testing.T) {
		t.Parallel()

		line := strings.Repeat("word ", 30)
		for _, got := range strings.Split((Styler{}).Wrap(line, 0), "\n") {
			if len(got) > DefaultWidth {
				t.Fatalf("line %q is %d columns", got, len(got))
			}
		}
	})
}

func TestIndent(t *testing.T) {
	t.Parallel()

	if got := (Styler{}).Indent("a\nb", 2); got != "  a\n  b" {
		t.Fatalf("indented = %q", got)
	}

	if got := (Styler{}).Indent("a\n\nb", 2); got != "  a\n\n  b" {
		t.Fatalf("a blank line must stay blank, got %q", got)
	}
}

func TestErrorf(t *testing.T) {
	t.Parallel()

	err := errors.New("that key was rejected")

	t.Run("plain, no hint", func(t *testing.T) {
		t.Parallel()

		if got := (Styler{}).Errorf(err, ""); got != "error: that key was rejected" {
			t.Fatalf("rendered = %q", got)
		}
	})

	t.Run("plain, with a hint", func(t *testing.T) {
		t.Parallel()

		want := "error: that key was rejected\n\n  > run `reevit login`"
		if got := (Styler{}).Errorf(err, "run `reevit login`"); got != want {
			t.Fatalf("rendered = %q, want %q", got, want)
		}
	})

	t.Run("coloured", func(t *testing.T) {
		t.Parallel()

		rich := New(Options{IsTerminal: true, Env: utf8Env})

		want := "\x1b[31merror:\x1b[0m that key was rejected\n\n  \x1b[36m→\x1b[0m run `reevit login`"
		if got := rich.Errorf(err, "run `reevit login`"); got != want {
			t.Fatalf("rendered = %q, want %q", got, want)
		}
	})

	t.Run("nil is nothing", func(t *testing.T) {
		t.Parallel()

		if got := (Styler{}).Errorf(nil, "ignored"); got != "" {
			t.Fatalf("rendered = %q, want empty", got)
		}
	})
}

// TestNoBareReset — a reset with no opening sequence leaks into whatever the
// terminal prints next.
func TestNoBareReset(t *testing.T) {
	t.Parallel()

	rich := New(Options{IsTerminal: true, Env: utf8Env})

	for _, rendered := range []string{
		rich.Success("ok"), rich.Failure("no"), rich.Warning("hm"), rich.Note("fyi"),
		rich.Step("go"), rich.Pending("later"), rich.Bold("t"), rich.Dim("t"),
		rich.Accent("t"), rich.Heading("H"), rich.Command("c"), rich.URL("https://x"),
	} {
		if strings.Count(rendered, "\x1b[0m") != strings.Count(rendered, "\x1b[")-strings.Count(rendered, "\x1b[0m") {
			t.Errorf("%q does not pair its escapes", rendered)
		}
	}

	// An empty run must not emit an escape pair at all.
	if got := rich.Bold(""); got != "" {
		t.Errorf("Bold(\"\") = %q, want empty", got)
	}
}
