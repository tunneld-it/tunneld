package attach

import "testing"

// TestCommandMarks pins what the shell's marks (OSC 133) and Run's typing
// add up to: a D before any prompt ends nothing; a line typed runs until the
// first D after it, which is how it exited; a line typed, or a C, since the
// last prompt is a command running; a cursor Run did not answer is unknown,
// and so is everything for a shell that has marked no prompt.
func TestCommandMarks(t *testing.T) {
	o := newOutput(outputSize)
	if _, ok := o.command(o.typed(o.offset())); ok {
		t.Fatal("a shell that marked nothing knows a command")
	}
	if _, busy := o.running(); busy {
		t.Fatal("a shell that marked nothing is running a command")
	}

	o.osc133([]byte("D;0"))
	o.osc133([]byte("A"))
	if _, busy := o.running(); busy {
		t.Fatal("at a prompt, running")
	}

	first := o.typed(o.offset())
	c, ok := o.command(first)
	if !ok || c.Running == nil || !*c.Running || c.ExitCode != nil || c.CommandCursor != first {
		t.Fatalf("command(%s) just typed = %+v, %v; want running, no exit code", first, c, ok)
	}
	if busy, ok := o.running(); !ok || busy.CommandCursor != first {
		t.Errorf("running() after a line typed = %+v, %v; want it, at its cursor", busy, ok)
	}

	o.osc133([]byte("C"))
	if busy, ok := o.running(); !ok || busy.CommandCursor != first {
		t.Errorf("running() once it started = %+v, %v; want the line typed", busy, ok)
	}
	o.osc133([]byte("D;7"))
	o.osc133([]byte("A"))
	if c, _ := o.command(first); !c.Ended() || *c.ExitCode != 7 {
		t.Errorf("command(%s) after D;7 = %+v, want ended 7", first, c)
	}
	if _, busy := o.running(); busy {
		t.Error("back at a prompt, running")
	}

	_, _ = o.Write([]byte("its output\r\n"))
	o.osc133([]byte("C"))
	if busy, ok := o.running(); !ok || busy.CommandCursor == first {
		t.Errorf("running() for a command a person started = %+v, %v; want one, at its own cursor", busy, ok)
	}
	o.osc133([]byte("D;0"))

	if _, ok := o.command("not one Run answered"); ok {
		t.Error("a cursor Run did not answer is known")
	}

	o.reset()
	if _, ok := o.command(first); ok {
		t.Error("after the program started again, before its prompt, a command is known")
	}
	kept := len(o.marks)
	o.osc133([]byte("D;9"))
	if len(o.marks) != kept {
		t.Error("the restarted shell's first D, before its prompt, was kept")
	}
}

// TestMarksAreBounded pins how many marks a log keeps.
func TestMarksAreBounded(t *testing.T) {
	o := newOutput(outputSize)
	o.osc133([]byte("A"))
	for range 2 * maxMarks {
		o.osc133([]byte("C"))
	}
	if len(o.marks) != maxMarks {
		t.Errorf("kept %d marks, want %d", len(o.marks), maxMarks)
	}
}
