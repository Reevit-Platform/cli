package cmd

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestChoose(t *testing.T) {
	t.Parallel()

	options := []string{"webhook", "checkout", "client"}

	tests := map[string]struct {
		input   string
		multi   bool
		want    []int
		wantErr string
	}{
		"enter picks the first option":  {input: "\n", want: []int{0}},
		"single pick":                   {input: "2\n", want: []int{1}},
		"comma separated":               {input: "1,3\n", multi: true, want: []int{0, 2}},
		"space separated":               {input: "1 3\n", multi: true, want: []int{0, 2}},
		"duplicates collapse":           {input: "1,1,3\n", multi: true, want: []int{0, 2}},
		"surrounding spaces are fine":   {input: "  2  \n", want: []int{1}},
		"zero is out of range":          {input: "0\n", wantErr: "invalid choice"},
		"above the last option":         {input: "4\n", wantErr: "invalid choice"},
		"non numeric":                   {input: "webhook\n", wantErr: "invalid choice"},
		"two picks when only one fits":  {input: "1,2\n", wantErr: "pick exactly one option"},
		"no trailing newline still ok":  {input: "3", want: []int{2}},
		"eof with no input is an error": {input: "", wantErr: "read choice"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			got, err := choose(&out, strings.NewReader(tc.input), "What do you need?", options, tc.multi)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("choose() = %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("picks = %v, want %v", got, tc.want)
			}

			// The menu itself is part of the contract: every option is
			// numbered from 1 so the answers above mean what they say.
			for i, option := range options {
				if !strings.Contains(out.String(), option) {
					t.Errorf("menu is missing option %d (%s):\n%s", i+1, option, out.String())
				}
			}
		})
	}
}

func TestChooseEOFWrapsIOEOF(t *testing.T) {
	t.Parallel()

	_, err := choose(io.Discard, strings.NewReader(""), "pick", []string{"a"}, false)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want it to wrap io.EOF", err)
	}
}

func TestPromptString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		def     string
		want    string
		wantErr bool
	}{
		"enter returns the default":     {input: "\n", def: "app/api/webhooks", want: "app/api/webhooks"},
		"input wins over the default":   {input: "  src/hooks  \n", def: "app/api", want: "src/hooks"},
		"empty default stays empty":     {input: "\n", want: ""},
		"no trailing newline still ok":  {input: "src/hooks", def: "app/api", want: "src/hooks"},
		"eof with no input is an error": {input: "", def: "app/api", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := promptString(io.Discard, strings.NewReader(tc.input), "Where?", tc.def)

			if tc.wantErr {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("error = %v, want it to wrap io.EOF", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("promptString() = %v", err)
			}

			if got != tc.want {
				t.Fatalf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPromptStringShowsTheDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	if _, err := promptString(&out, strings.NewReader("\n"), "Where?", "app/api"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "[app/api]") {
		t.Fatalf("prompt = %q, want it to show the default", out.String())
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		def     bool
		want    bool
		wantErr bool
	}{
		"enter takes the true default":  {input: "\n", def: true, want: true},
		"enter takes the false default": {input: "\n", def: false, want: false},
		"y":                             {input: "y\n", def: false, want: true},
		"yes":                           {input: "YES\n", def: false, want: true},
		"no":                            {input: "no\n", def: true, want: false},
		"garbage is not a yes":          {input: "maybe\n", def: true, want: false},
		"eof with no input is an error": {input: "", def: true, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := confirm(io.Discard, strings.NewReader(tc.input), "Apply?", tc.def)

			if tc.wantErr {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("error = %v, want it to wrap io.EOF", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("confirm() = %v", err)
			}

			if got != tc.want {
				t.Fatalf("answer = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfirmShowsTheDefaultInTheSuffix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		def  bool
		want string
	}{{def: true, want: "[Y/n]"}, {def: false, want: "[y/N]"}} {
		var out bytes.Buffer

		if _, err := confirm(&out, strings.NewReader("\n"), "Apply?", tc.def); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("prompt = %q, want it to contain %q", out.String(), tc.want)
		}
	}
}

// TestReadPromptLineDoesNotSwallowTheNextAnswer is the property the comment on
// readPromptLine protects: a buffered reader per prompt would read ahead and
// strand every following piped answer in a discarded buffer.
func TestReadPromptLineDoesNotSwallowTheNextAnswer(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("first\nsecond\n")

	one, err := promptString(io.Discard, in, "One?", "")
	if err != nil {
		t.Fatal(err)
	}

	two, err := promptString(io.Discard, in, "Two?", "")
	if err != nil {
		t.Fatal(err)
	}

	if one != "first" || two != "second" {
		t.Fatalf("answers = %q, %q; want \"first\", \"second\"", one, two)
	}
}

func TestReadPromptLineAcrossPromptKinds(t *testing.T) {
	t.Parallel()

	// choose, then confirm, then promptString — all sharing one stdin, the
	// way a piped `reevit init` session feeds them.
	in := strings.NewReader("2\ny\nsrc/reevit\n")

	picks, err := choose(io.Discard, in, "Pick", []string{"a", "b"}, false)
	if err != nil || !slices.Equal(picks, []int{1}) {
		t.Fatalf("choose() = %v, %v; want [1], nil", picks, err)
	}

	ok, err := confirm(io.Discard, in, "Apply?", false)
	if err != nil || !ok {
		t.Fatalf("confirm() = %v, %v; want true, nil", ok, err)
	}

	where, err := promptString(io.Discard, in, "Where?", "lib")
	if err != nil || where != "src/reevit" {
		t.Fatalf("promptString() = %q, %v; want \"src/reevit\", nil", where, err)
	}
}
