package cli

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The two commands goldie's SessionStart hook runs. A hook is goldie's when its
// command equals one of them. A test parses both against the real flag sets.
const (
	HookContextCommand = "goldie context --hook"
	HookPullCommand    = "goldie pull --quiet --if-pulled"
)

// HookStatus describes the goldie hook InstallSessionStartHook found in the file.
type HookStatus int

const (
	// HookAdded: the file held no goldie hook.
	HookAdded HookStatus = iota
	// HookCurrent: the file held goldie's group as wanted; no byte changes.
	HookCurrent
	// HookUpdated: goldie's group was there but lacked one of the two hooks.
	HookUpdated
)

// hookRefusal is a file goldie hooks install will not edit: what is wrong and
// the next step, on two lines. It never holds the parser's text or a path.
type hookRefusal struct{ what, next string }

func (e hookRefusal) Error() string { return e.what + "\n" + e.next }

const (
	settingsFile = ".claude/settings.json"
	fixThenRerun = "Fix the file by hand, then run goldie hooks install again."
)

var (
	errHookInvalid = hookRefusal{settingsFile + " is not valid JSON (goldie edits no file with comments, a byte order mark or trailing commas)", fixThenRerun}
	errHookDup     = hookRefusal{settingsFile + " holds a duplicate key, so goldie cannot tell which one counts", fixThenRerun}
	errHookObject  = hookRefusal{settingsFile + " is not a JSON object", fixThenRerun}
	errHookShape   = hookRefusal{"hooks in " + settingsFile + " are not shaped as Claude Code expects (hooks an object, SessionStart an array of groups, each with a hooks array of commands)", fixThenRerun}
	errHookTangled = hookRefusal{settingsFile + ": goldie's hooks under hooks.SessionStart are repeated or split over several groups", "Remove goldie's entries from hooks.SessionStart by hand, then run goldie hooks install again."}
)

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookGroup struct {
	Hooks []hookEntry `json:"hooks"`
}

var (
	ctxHook  = hookEntry{"command", HookContextCommand}
	pullHook = hookEntry{"command", HookPullCommand}
)

// InstallSessionStartHook returns file with goldie's SessionStart group under
// hooks.SessionStart: one matcher group with the two command hooks. file is
// the content of .claude/settings.json; empty (or only whitespace) is a missing
// file. The edit only inserts, so every byte of file stays: keys, order,
// indentation, line endings and the user's own hooks. A file that has goldie's
// group as wanted comes back unchanged (HookCurrent). A file it cannot edit
// safely is refused with an error that says what is wrong and the next step,
// and no bytes are returned.
//
// encoding/json only validates; the offsets come from a scan of the bytes, so
// the edit is a splice, not a re-marshal.
func InstallSessionStartHook(file []byte) ([]byte, HookStatus, error) {
	group := hookGroup{[]hookEntry{ctxHook, pullHook}}
	if len(bytes.Trim(file, " \t\r\n")) == 0 {
		doc, _ := json.MarshalIndent(map[string]any{"hooks": map[string]any{"SessionStart": []hookGroup{group}}}, "", "  ")
		return append(append(bytes.Clone(file), doc...), '\n'), HookAdded, nil
	}
	if !json.Valid(file) {
		return nil, 0, errHookInvalid
	}
	s := scan{b: file}
	top := s.value(s.skipWS(0))
	if file[top.start] != '{' {
		return nil, 0, errHookObject
	}
	if s.hasDuplicateKey(top) {
		return nil, 0, errHookDup
	}
	hooks, ok, err := lookup(s.object(top), "hooks")
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		add := func(l layout) string { return l.member("hooks", map[string]any{"SessionStart": []hookGroup{group}}) }
		return s.insert(top, 0, false, add), HookAdded, nil
	}
	if file[hooks.start] != '{' {
		return nil, 0, errHookShape
	}
	start, ok, err := lookup(s.object(hooks), "SessionStart")
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		add := func(l layout) string { return l.member("SessionStart", []hookGroup{group}) }
		return s.insert(hooks, 1, false, add), HookAdded, nil
	}
	if file[start.start] != '[' {
		return nil, 0, errHookShape
	}

	// Count goldie's commands in every hook of every group, and remember the
	// hooks array of each group that holds one.
	var owners []node
	var ctxN, pullN int
	for _, g := range s.array(start) {
		if file[g.start] != '{' {
			return nil, 0, errHookShape
		}
		arr, ok, err := lookup(s.object(g), "hooks")
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			continue
		}
		if file[arr.start] != '[' {
			return nil, 0, errHookShape
		}
		var c, p int
		for _, e := range s.array(arr) {
			if file[e.start] != '{' {
				return nil, 0, errHookShape
			}
			cmd, ok, err := lookup(s.object(e), "command")
			if err != nil {
				return nil, 0, err
			}
			if !ok {
				continue
			}
			if file[cmd.start] != '"' {
				return nil, 0, errHookShape
			}
			switch s.str(cmd) {
			case HookContextCommand:
				c++
			case HookPullCommand:
				p++
			}
		}
		if c+p > 0 {
			owners = append(owners, arr)
			ctxN, pullN = ctxN+c, pullN+p
		}
	}
	switch {
	case ctxN == 0 && pullN == 0:
		return s.insert(start, 2, false, func(l layout) string { return l.value(group) }), HookAdded, nil
	case ctxN > 1 || pullN > 1 || len(owners) != 1:
		return nil, 0, errHookTangled
	case ctxN == 1 && pullN == 1:
		return bytes.Clone(file), HookCurrent, nil
	case ctxN == 1: // the pull hook is missing: it goes last
		return s.insert(owners[0], 4, false, func(l layout) string { return l.value(pullHook) }), HookUpdated, nil
	default: // the context hook is missing: it goes first
		return s.insert(owners[0], 4, true, func(l layout) string { return l.value(ctxHook) }), HookUpdated, nil
	}
}

// node is a JSON value at b[start:end].
type node struct{ start, end int }

// member is a key, its decoded text, the offset the member starts at, and its value.
type member struct {
	key   string
	start int
	val   node
}

// lookup returns the value of key in ms. A key that differs from it only in
// case is refused: encoding/json reads both as one field and Claude Code as two.
func lookup(ms []member, key string) (v node, ok bool, err error) {
	for _, m := range ms {
		switch {
		case m.key == key:
			v, ok = m.val, true
		case strings.EqualFold(m.key, key):
			return node{}, false, errHookDup
		}
	}
	return v, ok, nil
}

// scan walks bytes that json.Valid accepted, so it checks no syntax.
type scan struct{ b []byte }

func (s *scan) skipWS(i int) int {
	for i < len(s.b) && strings.IndexByte(" \t\r\n", s.b[i]) >= 0 {
		i++
	}
	return i
}

// value returns the value that starts at i.
func (s *scan) value(i int) node {
	end := i
	switch s.b[i] {
	case '"':
		end = s.strEnd(i)
	case '{', '[':
		depth := 0
		for ; end < len(s.b); end++ {
			switch s.b[end] {
			case '"':
				end = s.strEnd(end) - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
			if depth == 0 {
				end++
				break
			}
		}
	default:
		for end < len(s.b) && strings.IndexByte(" \t\r\n,]}", s.b[end]) < 0 {
			end++
		}
	}
	return node{i, end}
}

// strEnd is the offset after the string that starts at i.
func (s *scan) strEnd(i int) int {
	for i++; s.b[i] != '"'; i++ {
		if s.b[i] == '\\' {
			i++
		}
	}
	return i + 1
}

// str is the decoded string value n.
func (s *scan) str(n node) string {
	var v string
	_ = json.Unmarshal(s.b[n.start:n.end], &v)
	return v
}

// object returns the members of the object n.
func (s *scan) object(n node) []member {
	var ms []member
	for i := s.skipWS(n.start + 1); s.b[i] != '}'; i = s.skipWS(i) {
		k := s.value(i)
		v := s.value(s.skipWS(s.skipWS(k.end) + 1))
		ms = append(ms, member{s.str(k), i, v})
		if i = s.skipWS(v.end); s.b[i] == ',' {
			i++
		}
	}
	return ms
}

// array returns the elements of the array n.
func (s *scan) array(n node) []node {
	var ns []node
	for i := s.skipWS(n.start + 1); s.b[i] != ']'; i = s.skipWS(i) {
		v := s.value(i)
		ns = append(ns, v)
		if i = s.skipWS(v.end); s.b[i] == ',' {
			i++
		}
	}
	return ns
}

// hasDuplicateKey reports whether any object under n repeats a key, as the
// decoded string: "a" and "a" are the same key.
func (s *scan) hasDuplicateKey(n node) bool {
	switch s.b[n.start] {
	case '{':
		seen := map[string]bool{}
		for _, m := range s.object(n) {
			if seen[m.key] || s.hasDuplicateKey(m.val) {
				return true
			}
			seen[m.key] = true
		}
	case '[':
		for _, e := range s.array(n) {
			if s.hasDuplicateKey(e) {
				return true
			}
		}
	}
	return false
}

// layout renders inserted text in the style of its neighbours: indented like
// the element beside it when that element is on a line of its own, else compact.
type layout struct {
	multiline        bool
	indent, unit, nl string
}

func (l layout) value(v any) string {
	var b []byte
	if l.multiline {
		b, _ = json.MarshalIndent(v, l.indent, l.unit)
	} else {
		b, _ = json.Marshal(v)
	}
	return strings.ReplaceAll(string(b), "\n", l.nl)
}

func (l layout) member(key string, v any) string {
	k, _ := json.Marshal(key)
	if l.multiline {
		return string(k) + ": " + l.value(v)
	}
	return string(k) + ":" + l.value(v)
}

// insert adds one element, which text renders, as the last element of the
// object or array c (the first when front is set) and returns the new bytes.
// depth is how deep c lies in the document, to guess the indent unit.
func (s *scan) insert(c node, depth int, front bool, text func(layout) string) []byte {
	var starts, ends []int
	if s.b[c.start] == '{' {
		for _, m := range s.object(c) {
			starts, ends = append(starts, m.start), append(ends, m.val.end)
		}
	} else {
		for _, e := range s.array(c) {
			starts, ends = append(starts, e.start), append(ends, e.end)
		}
	}
	if len(starts) == 0 {
		return splice(s.b, c.start+1, text(layout{nl: "\n"}), "", "")
	}
	ref := len(starts) - 1
	if front {
		ref = 0
	}
	ws := starts[ref]
	for ws > c.start+1 && strings.IndexByte(" \t\r\n", s.b[ws-1]) >= 0 {
		ws--
	}
	run := string(s.b[ws:starts[ref]])
	l := layout{nl: "\n"}
	if i := strings.LastIndexByte(run, '\n'); i >= 0 {
		l.multiline, l.indent, l.unit = true, run[i+1:], "  "
		if strings.Contains(run, "\r\n") {
			l.nl = "\r\n"
		}
		if n := len(l.indent); n > 0 && n%(depth+1) == 0 && strings.Trim(l.indent, l.indent[:1]) == "" {
			l.unit = l.indent[:n/(depth+1)]
		}
	}
	if front {
		return splice(s.b, starts[ref], text(l), "", ","+run)
	}
	return splice(s.b, ends[ref], text(l), ","+run, "")
}

// splice returns b with before+text+after inserted at pos.
func splice(b []byte, pos int, text, before, after string) []byte {
	out := make([]byte, 0, len(b)+len(text)+len(before)+len(after))
	out = append(out, b[:pos]...)
	out = append(out, before...)
	out = append(out, text...)
	out = append(out, after...)
	return append(out, b[pos:]...)
}
