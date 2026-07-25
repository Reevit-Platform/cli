package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// choose renders a numbered menu and returns the indexes the user picked.
// Accepts "1", "1,3", "1 3", or Enter for the default (first option when
// none marked). multi=false accepts exactly one pick.
func choose(out io.Writer, in io.Reader, title string, options []string, multi bool) ([]int, error) {
	fmt.Fprintf(out, "\n%s\n", title)

	for i, opt := range options {
		fmt.Fprintf(out, "  %d) %s\n", i+1, opt)
	}

	hint := "choice"
	if multi {
		hint = "choices, e.g. 1,2"
	}

	fmt.Fprintf(out, "  [%s — Enter for 1] ", hint)

	line, err := readPromptLine(in)
	if err != nil && line == "" {
		return nil, fmt.Errorf("read choice: %w", err)
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return []int{0}, nil
	}

	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' })

	var picks []int

	seen := map[int]bool{}

	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || n > len(options) {
			return nil, fmt.Errorf("invalid choice %q — pick a number between 1 and %d", f, len(options))
		}

		if !seen[n-1] {
			seen[n-1] = true
			picks = append(picks, n-1)
		}
	}

	if !multi && len(picks) > 1 {
		return nil, fmt.Errorf("pick exactly one option")
	}

	return picks, nil
}

// promptString asks for a free-text value; Enter returns def.
func promptString(out io.Writer, in io.Reader, question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s] ", question, def)
	} else {
		fmt.Fprintf(out, "%s ", question)
	}

	line, err := readPromptLine(in)
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}

	return line, nil
}

// confirm asks a yes/no question; Enter means the given default.
func confirm(out io.Writer, in io.Reader, question string, def bool) (bool, error) {
	suffix := "[Y/n]"
	if !def {
		suffix = "[y/N]"
	}

	fmt.Fprintf(out, "%s %s ", question, suffix)

	line, err := readPromptLine(in)
	if err != nil && line == "" {
		return false, fmt.Errorf("read answer: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// readPromptLine deliberately reads one byte at a time. Creating a new
// bufio.Reader for every question can consume later answers from piped stdin
// and strand them in a discarded buffer.
func readPromptLine(in io.Reader) (string, error) {
	var line strings.Builder
	var one [1]byte

	for {
		n, err := in.Read(one[:])
		if n == 1 {
			if one[0] == '\n' {
				return line.String(), nil
			}
			line.WriteByte(one[0])
		}
		if err != nil {
			if err == io.EOF && line.Len() > 0 {
				return line.String(), nil
			}
			return line.String(), err
		}
	}
}
