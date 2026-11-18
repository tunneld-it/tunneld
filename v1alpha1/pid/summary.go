package pid

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Style is how the summary and the list are painted: Color for ANSI styles,
// where stderr is a terminal that wants them, and Columns for the width the
// prose is wrapped to, 78 at most.
type Style struct {
	Color   bool
	Columns int
}

func (s Style) paint(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) bold(text string) string { return s.paint("1", text) }
func (s Style) dim(text string) string  { return s.paint("2", text) }
func (s Style) cyan(text string) string { return s.paint("36", text) }

// Addresses is what run is printed on stdout, one per line, the way a run
// prints them: the panel's address alone when there is a panel, otherwise one
// per origin, routed by ?n when there is more than one. None with no cached
// settings.
func Addresses(run Run) []string {
	host := run.Env["LIBTUNNEL_HOSTNAME"]
	if host == "" {
		return nil
	}
	base := "https://" + host + "/"
	origins := split(run.Env["TUNNELD_ORIGINS"])
	if len(origins) <= 1 || run.Env["TUNNELD_MULTIVIEW"] == "true" {
		return []string{base}
	}
	out := make([]string, len(origins))
	for i := range origins {
		out[i] = base + "?" + strconv.Itoa(i)
	}
	return out
}

// Summary is what a run left in the background is handed back with: the
// directory it runs in, its pid and its log, then what it shares and the
// address each origin answers on. already says it was running before this.
func Summary(w io.Writer, run Run, already bool, s Style) {
	state := "now"
	if already {
		state = "already"
	}
	lines := []string{"", s.bold("🚇 tunneld is " + state + " running in the background"), ""}
	row := func(name, value string) { lines = append(lines, "    "+s.dim(name)+"  "+value) }
	if pwd := run.Env["PWD"]; pwd != "" {
		row("cwd", home(pwd))
	}
	row("pid", strconv.Itoa(run.Pid))
	if run.LogPath != "" {
		row("log", home(run.LogPath))
	}
	lines = append(lines, "")

	host := run.Env["LIBTUNNEL_HOSTNAME"]
	if host == "" {
		lines = append(lines, "  Your application is now available at the address above.", "")
	} else {
		base := "https://" + host + "/"
		origins := split(run.Env["TUNNELD_ORIGINS"])
		columns := s.Columns
		if columns <= 0 || columns > 78 {
			columns = 78
		}
		lines = append(lines, prose(host, origins, columns, s)...)
		lines = append(lines, "")
		arrow := s.dim("→")
		if len(origins) <= 1 {
			first := ""
			if len(origins) == 1 {
				first = origins[0]
			}
			lines = append(lines, "  "+s.cyan(base)+"  "+arrow+" "+label(first))
		} else {
			width := len(base + "?" + strconv.Itoa(len(origins)-1))
			if run.Env["TUNNELD_MULTIVIEW"] == "true" {
				lines = append(lines, "  "+s.cyan(pad(base, width))+"  "+arrow+" multiview")
			}
			for i, o := range origins {
				lines = append(lines, "  "+s.cyan(pad(base+"?"+strconv.Itoa(i), width))+"  "+arrow+" "+label(o))
			}
		}
		lines = append(lines, "")
	}
	fmt.Fprint(w, strings.Join(lines, "\n")+"\n")
}

// List prints every run on w, a line each: its pid, then its address,
// directory and origins from its cached settings, or its key when there are
// none.
func List(w io.Writer, runs []Run) {
	width := 0
	for _, r := range runs {
		width = max(width, len(strconv.Itoa(r.Pid)))
	}
	for _, r := range runs {
		fields := []string{pad(strconv.Itoa(r.Pid), width)}
		if host := r.Env["LIBTUNNEL_HOSTNAME"]; host != "" {
			fields = append(fields, "https://"+host+"/")
			if pwd := r.Env["PWD"]; pwd != "" {
				fields = append(fields, home(pwd))
			}
			var labels []string
			for _, o := range split(r.Env["TUNNELD_ORIGINS"]) {
				labels = append(labels, label(o))
			}
			if len(labels) > 0 {
				fields = append(fields, strings.Join(labels, ", "))
			}
		} else {
			fields = append(fields, r.Key)
		}
		fmt.Fprintln(w, strings.Join(fields, "  "))
	}
}

// listed is a run as -l -s prints it: Env is the path of its settings file,
// never what it holds, since the spec is among them.
type listed struct {
	Pid       int      `json:"pid"`
	Key       string   `json:"key"`
	Address   string   `json:"address,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Origins   []string `json:"origins,omitempty"`
	Env       string   `json:"env,omitempty"`
	Log       string   `json:"log,omitempty"`
}

// ListJSON prints every run on w as one JSON array, [] for none: each run's
// pid and key, then its address, the address of each origin, its directory
// and origins from its cached settings, and the paths of its settings and log,
// those it has, every path absolute.
func ListJSON(w io.Writer, runs []Run) error {
	out := make([]listed, 0, len(runs))
	for _, r := range runs {
		l := listed{Pid: r.Pid, Key: r.Key, Cwd: absolute(r.Env["PWD"]), Origins: split(r.Env["TUNNELD_ORIGINS"]), Env: absolute(r.EnvPath), Log: absolute(r.LogPath)}
		if host := r.Env["LIBTUNNEL_HOSTNAME"]; host != "" {
			l.Address = "https://" + host + "/"
			l.Addresses = Addresses(r)
		}
		out = append(out, l)
	}
	return json.NewEncoder(w).Encode(out)
}

// prose is what the run shares, said as a sentence: the origins, the address
// they are tunneled through, and that the list below is where each one
// answers. In lines of at most columns, indent included.
func prose(host string, origins []string, columns int, s Style) []string {
	type word struct{ text, shown string }
	plain := func(text string) []word {
		var out []word
		for _, w := range strings.Split(text, " ") {
			out = append(out, word{w, w})
		}
		return out
	}
	one := len(origins) == 1
	labels := make([]string, len(origins))
	for i, o := range origins {
		labels[i] = label(o)
	}
	base := "https://" + host + "/"
	var words []word
	words = append(words, plain(choose(one, "Your application", "Your applications"))...)
	words = append(words, plain("("+strings.Join(labels, ", ")+")")...)
	words = append(words, plain(choose(one, "is now available, tunneled through", "are now available, tunneled through"))...)
	words = append(words, word{base + ",", s.cyan(base) + ","})
	words = append(words, plain(choose(one, "at the following address:", "at the following addresses:"))...)

	width := max(columns, 24) - 2
	var lines, line []string
	used := 0
	for _, w := range words {
		if len(line) > 0 && used+1+len(w.text) > width {
			lines = append(lines, "  "+strings.Join(line, " "))
			line, used = nil, 0
		}
		if len(line) > 0 {
			used++
		}
		used += len(w.text)
		line = append(line, w.shown)
	}
	if len(line) > 0 {
		lines = append(lines, "  "+strings.Join(line, " "))
	}
	return lines
}

// label is an origin as a person reads it: a program as its path and
// arguments rather than the exec:// URL carrying them.
func label(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "exec" {
		return origin
	}
	words := append([]string{u.Path}, u.Query()["arg"]...)
	for i, w := range words {
		words[i] = quote(w)
	}
	return strings.Join(words, " ")
}

var safe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

// quote spells a word so that pasting it back into a shell gives the same
// word: as it is when nothing in it is special, single-quoted otherwise, or
// double-quoted for cmd.exe.
func quote(word string) string {
	if safe.MatchString(word) {
		return word
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(word, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// home spells a path under $HOME the short way.
func home(p string) string {
	h := os.Getenv("HOME")
	if h != "" && strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}

// split is a comma-separated list without its empty parts.
func split(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// absolute is path made absolute against the working directory, "" for "".
func absolute(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// pad is text padded with spaces to width.
func pad(text string, width int) string {
	if n := width - len(text); n > 0 {
		return text + strings.Repeat(" ", n)
	}
	return text
}

func choose(one bool, a, b string) string {
	if one {
		return a
	}
	return b
}
