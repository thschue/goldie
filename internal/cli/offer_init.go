package cli

import (
	"context"
	"errors"
	"io"

	"github.com/thschue/goldie/internal/contract/project"
)

// offerQuestion is what goldie server --dev asks once it is serving.
const offerQuestion = "Set up this repository for this dev server now?"

// OfferInit is the offer of goldie server --dev: set up the repository it runs
// in for the dev server at addr with token. It asks only when env.In is a
// terminal (cmd sets it only when stdin and stderr are terminals), the working
// directory is in a git repository that is not a Source repository, and
// .goldie/config.yaml does not exist; otherwise it prints, reads and writes
// nothing. On yes it runs the guided init, with addr and token as the
// environment's GOLDIE_ADDR and GOLDIE_TOKEN: a lookup overlay, never
// os.Setenv. The whole flow writes to env.Err, so env.Out, the export line of
// --dev, stays untouched. No and the end of input return nil. A cancelled ctx
// ends the wait for an answer at once.
//
// Errors never carry the token: init words them without it.
func OfferInit(ctx context.Context, env Env, addr, token string) error {
	if env.In == nil {
		return nil
	}
	errOut := env.Err
	if errOut == nil {
		errOut = io.Discard
	}
	env.Lookup = overlay(env.Lookup, map[string]string{EnvAddr: addr, EnvToken: token})
	env.Out = errOut
	env.In = &ctxReader{ctx: ctx, src: env.In}
	c, err := (&ClientFlags{}).commander(env, tokenRequired)
	if err != nil {
		return err
	}
	root, proj, err := c.initDetect()
	if err != nil || initPreflight(root) != nil || project.IsSourceRepository(root) {
		return nil // the offer does not apply here
	}
	p := newPrompter(env.In, errOut)
	yes, err := p.confirm(offerQuestion)
	switch {
	case errors.Is(err, errPromptEOF), errors.Is(err, errPromptCut), ctx.Err() != nil:
		return nil
	case err != nil:
		return err
	case !yes:
		return nil
	}
	return c.initGuided(ctx, root, proj, p)
}

// overlay is lookup with set's names fixed, and the settings that would
// compete with them (a token file, a content directory) unset.
func overlay(lookup func(string) (string, bool), set map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if v, ok := set[name]; ok {
			return v, true
		}
		switch name {
		case EnvToken + "_FILE", EnvDir:
			return "", false
		}
		return lookup(name)
	}
}

// ctxReader reads from src, and returns ctx's error as soon as ctx is done,
// even while src blocks. A read that src has not answered yet stays pending
// for the next Read, and its bytes are not lost.
type ctxReader struct {
	ctx     context.Context
	src     io.Reader
	pending chan readResult
	buf     []byte
}

type readResult struct {
	b   []byte
	err error
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		if r.pending == nil {
			r.pending = make(chan readResult, 1)
			go func(ch chan<- readResult) {
				b := make([]byte, maxAnswer)
				n, err := r.src.Read(b)
				ch <- readResult{b[:n], err}
			}(r.pending)
		}
		select {
		case res := <-r.pending:
			r.pending = nil
			if len(res.b) == 0 {
				return 0, res.err
			}
			r.buf = res.b
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
