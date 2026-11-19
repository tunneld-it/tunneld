package attach

import (
	"slices"
	"strconv"
	"strings"
)

// Command is a command line in a shell that marks its commands with OSC 133:
// where it was typed, whether it still runs, and how it exited.
type Command struct {
	// CommandCursor is where the command line was typed: its output is read
	// from here.
	CommandCursor string `json:"command_cursor,omitempty" jsonschema:"where the command line was typed: pass it to terminal_read or terminal_wait as cursor to read its output from the start and learn how it ended"`
	// Running is whether it has not ended yet.
	Running *bool `json:"running,omitempty" jsonschema:"whether the command is still running"`
	// ExitCode is how it ended, nil while it runs.
	ExitCode *int `json:"exit_code,omitempty" jsonschema:"how the command exited, once it has: absent while it runs"`
}

// maxMarks bounds how many marks a log keeps: the commands an agent can still
// ask about.
const maxMarks = 512

// mark is one thing the shell's integration said (OSC 133 A, C or D), or a
// command line typed by Run, in the order they happened.
type mark struct {
	seq  int64
	kind byte // 'A' prompt, 'C' command started, 'D' command ended, 'T' typed by Run
	at   int64
	code int
	// typed is the cursor a 'T' mark answers to.
	typed string
}

// yes is a true to point at.
func yes() *bool { t := true; return &t }

// Ended is whether c has ended.
func (c Command) Ended() bool { return c.Running != nil && !*c.Running }

// osc133 records what an OSC 133 sequence says, in order with the output
// around it: the scanner hands it over between the writes before and after.
func (o *output) osc133(data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	kind, rest, _ := strings.Cut(string(data), ";")
	if len(kind) != 1 {
		return
	}
	switch k := kind[0]; k {
	case 'A':
		o.prompted = true
		o.addMark(mark{kind: 'A', at: o.end})
	case 'C':
		// What it prints starts after the line it was typed on, as Send's
		// input goes in after what is held.
		o.keepHeld()
		o.addMark(mark{kind: 'C', at: o.end})
	case 'D':
		// The prompt's first D, before any prompt was drawn, ends nothing.
		if !o.prompted {
			return
		}
		field, _, _ := strings.Cut(rest, ";")
		code, err := strconv.Atoi(field)
		if err != nil {
			code = 0
		}
		o.addMark(mark{kind: 'D', at: o.end, code: code})
		o.notify()
	}
}

// addMark appends m, numbered, keeping the last maxMarks. Called with mu held.
func (o *output) addMark(m mark) {
	o.seq++
	m.seq = o.seq
	o.marks = append(o.marks, m)
	if len(o.marks) > maxMarks {
		o.marks = append(o.marks[:0], o.marks[len(o.marks)-maxMarks:]...)
	}
}

// typed records a command line typed at offset at, and answers its cursor.
func (o *output) typed(at int64) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	cursor := o.cursor(at)
	o.addMark(mark{kind: 'T', at: at, typed: cursor})
	return cursor
}

// command is how the command line typed at cursor went: false when cursor
// is not one Run answered, or the log no longer keeps it.
func (o *output) command(cursor string) (Command, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.prompted {
		return Command{}, false
	}
	for i := len(o.marks) - 1; i >= 0; i-- {
		if o.marks[i].kind != 'T' || o.marks[i].typed != cursor {
			continue
		}
		c := Command{CommandCursor: cursor, Running: yes()}
		for _, m := range o.marks[i+1:] {
			if m.kind == 'D' {
				code, no := m.code, false
				c.Running, c.ExitCode = &no, &code
				break
			}
		}
		return c, true
	}
	return Command{}, false
}

// running is the command the shell is running now, and whether one is: a
// command started (C), or a line typed by Run, since the last prompt (A) and
// with no end (D) after it. Never, for a shell that has not marked a prompt.
func (o *output) running() (Command, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.prompted {
		return Command{}, false
	}
	for i := len(o.marks) - 1; i >= 0; i-- {
		switch m := o.marks[i]; m.kind {
		case 'D', 'A':
			return Command{}, false
		case 'T':
			return Command{CommandCursor: m.typed, Running: yes()}, true
		case 'C':
			// The line Run typed that started it, when there was one.
			for _, before := range slices.Backward(o.marks[:i]) {
				if before.kind == 'A' || before.kind == 'D' {
					break
				}
				if before.kind == 'T' {
					return Command{CommandCursor: before.typed, Running: yes()}, true
				}
			}
			return Command{CommandCursor: o.cursor(m.at), Running: yes()}, true
		}
	}
	return Command{}, false
}

// marked is whether the shell has marked a prompt: whether it says when its
// commands end.
func (o *output) marked() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.prompted
}
