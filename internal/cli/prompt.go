package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// errPromptEOF is the end of the input before an answer: Ctrl-D, or no input
// at all.
var errPromptEOF = errors.New("end of input")

const (
	// maxAnswer bounds one line of input; the rest of a longer line is dropped.
	maxAnswer = 4096
	// firstGrace is how long the first prompt waits for input before it shows
	// itself. Input that is already at its end (</dev/null, a closed pipe)
	// answers at once, and init then says nothing it did not say before there
	// were prompts; a terminal waits for the user, so the prompt shows.
	firstGrace = 200 * time.Millisecond
)

// prompter asks questions on out and reads the answers from in. One prompter
// serves a whole run: it owns the buffered reader, so no answer is lost
// between two questions.
type prompter struct {
	in    *bufio.Reader
	out   io.Writer
	asked bool
	// echoNL: the input is not a terminal, which would show the user's Enter
	// as a line break itself, so the prompter ends the prompt's line.
	echoNL bool
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	_, tty := in.(*os.File)
	return &prompter{in: bufio.NewReaderSize(in, maxAnswer), out: out, echoNL: !tty}
}

// ask prints question and returns the answer, trimmed and cleaned of control
// characters. It returns errPromptEOF when the input ends before any byte of
// an answer; on the first question that happens before anything is printed.
func (p *prompter) ask(question string) (string, error) {
	if !p.asked {
		p.asked = true
		ready := make(chan error, 1)
		go func() { _, err := p.in.Peek(1); ready <- err }()
		select {
		case err := <-ready:
			if err != nil {
				return "", errPromptEOF
			}
		case <-time.After(firstGrace):
			say(p.out, "%s", question)
			if err := <-ready; err != nil {
				return "", errPromptEOF
			}
			return p.answer()
		}
	}
	say(p.out, "%s", question)
	return p.answer()
}

// answer reads the answer to a question just printed.
func (p *prompter) answer() (string, error) {
	a, err := p.line()
	if p.echoNL {
		say(p.out, "\n")
	}
	return a, err
}

// line reads one line of at most maxAnswer bytes.
func (p *prompter) line() (string, error) {
	var b []byte
	for {
		part, err := p.in.ReadSlice('\n')
		if len(b) < maxAnswer {
			b = append(b, part...)
		}
		switch {
		case err == nil:
			return strings.TrimSpace(contract.CleanLine(string(b))), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case len(b) == 0:
			return "", errPromptEOF
		default: // the last line had no newline
			return strings.TrimSpace(contract.CleanLine(string(b))), nil
		}
	}
}

// say writes talk to w. A write that fails is not worth stopping for: the user
// would not see the reason either.
func say(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

// confirm asks a yes or no question whose Enter answer is yes. Anything else
// is asked again.
func (p *prompter) confirm(question string) (bool, error) {
	for {
		a, err := p.ask(question + " [Y/n] ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(a) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}
