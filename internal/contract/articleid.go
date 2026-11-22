package contract

import (
	"errors"
	"strings"
)

// ErrInvalidArticleID is returned by CheckArticleID. Callers match it with
// errors.Is and word their own hint; the message names no id, address or path.
var ErrInvalidArticleID = errors.New("article id is empty or made only of dots")

// CheckArticleID rejects ids that URL path cleaning would resolve away from
// the article path: the empty id and ids made only of dots ("." and "..").
// Whitespace is not trimmed, so " .. " is an ordinary (unknown) id.
func CheckArticleID(id string) error {
	if strings.Trim(id, ".") == "" {
		return ErrInvalidArticleID
	}
	return nil
}
