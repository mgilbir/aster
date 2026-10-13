package transforms

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	ecma262 "github.com/mgilbir/goecma262"
)

// CountPatternParams configures CountPattern.
type CountPatternParams struct {
	Field Field
	// Case is "upper", "lower" or "mixed" (the default) and is applied to the
	// text before matching.
	Case string
	// Pattern is the token regular expression, matched globally. Empty means
	// `[\w']+`: the operator's own fallback, which is what runs because the
	// parser does not apply the definition's `[\w"]+` default.
	Pattern string
	// Stopwords is a regular expression source; tokens fully matching it
	// (case-insensitively) are skipped.
	Stopwords string
	// As is the [text, count] output pair; empty entries default to "text" and
	// "count".
	As [2]string
}

// CountPattern tokenizes a text field with a regular expression and counts the
// tokens, generating one {text, count} tuple per distinct token. Order follows
// JavaScript object key order (integer-like tokens ascending first, then first
// appearance), because upstream iterates a plain object. Like upstream it
// fails on values that are not strings.
func CountPattern(ctx context.Context, data []jsval.Value, p CountPatternParams) ([]jsval.Value, error) {
	pattern := p.Pattern
	if pattern == "" {
		pattern = "[\\w']+"
	}
	// The two patterns are the call's while it runs, paid for before they
	// are parsed and compiled (see payPattern).
	led := budget.LedgerFrom(ctx)
	var held int64
	defer func() { led.Free(held) }()
	match, err := newCountPattern(ctx, pattern, "g", led, &held)
	if err != nil {
		return nil, err
	}
	stop, err := newCountPattern(ctx, "^"+p.Stopwords+"$", "i", led, &held)
	if err != nil {
		return nil, err
	}
	// The searches stop at the call's deadline. A copy made with
	// WithContext shares the program; the g pattern's lastIndex is not used
	// (FindAll searches the whole text).
	matchRe, stopRe := match.Re.WithContext(ctx), stop.Re.WithContext(ctx)
	textName, countName := p.As[0], p.As[1]
	if textName == "" {
		textName = "text"
	}
	if countName == "" {
		countName = "count"
	}
	var conv func(string) string
	switch p.Case {
	case "upper":
		conv = cases.Upper(language.Und).String
	case "lower":
		conv = cases.Lower(language.Und).String
	}
	counts := map[string]int{}
	var order []string
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		v := p.Field.Apply(t)
		if !v.IsStr() {
			return nil, fmt.Errorf("countpattern: value of %q is %s, not a string", p.Field.Name, v.Kind())
		}
		text := v.StrValue()
		if conv != nil {
			text = conv(text)
		}
		matchRe.SetMaxMemory(jsval.MatchMemory(led.Room(), len(text)))
		ms, err := matchRe.FindAllStringSubmatchIndexErr(text, -1)
		if err != nil {
			return nil, searchFailed(err)
		}
		for _, m := range ms {
			tok := text[m[0]:m[1]]
			stopRe.SetMaxMemory(jsval.MatchMemory(led.Room(), len(tok)))
			isStop, err := stopRe.MatchStringErr(tok)
			if err != nil {
				return nil, searchFailed(err)
			}
			if isStop {
				continue
			}
			if _, ok := counts[tok]; !ok {
				// Each distinct token is an output row: reserve them as they
				// are found, a thousand at a time.
				if len(order)&1023 == 0 {
					if err := reserveRows(ctx, len(order)+1024); err != nil {
						return nil, err
					}
				}
				order = append(order, tok)
			}
			counts[tok]++
		}
	}
	out := make([]jsval.Value, 0, len(order))
	for _, w := range OrderedKeys(order) {
		o := jsval.NewObject(2)
		o.Set(textName, jsval.Str(w))
		o.Set(countName, jsval.Int(counts[w]))
		out = append(out, jsval.Obj(o))
	}
	return out, nil
}

// newCountPattern compiles one of the operator's patterns under ctx, paid
// for with the ledger before its parse and its compile allocate, adding what
// it holds to *held: a pattern past what the ledger has left is refused
// unbuilt, and the deadline stops the compile. Its work is counted once it
// is built.
func newCountPattern(ctx context.Context, source, flags string, led *budget.Ledger, held *int64) (*jsval.Pattern, error) {
	p, err := jsval.NewPattern(ctx, source, flags, func(bytes int64) error {
		if bytes < 0 {
			led.Free(-bytes)
			*held += bytes
			return nil
		}
		if err := led.Alloc(bytes); err != nil {
			return fmt.Errorf("countpattern: a regular expression needs %d bytes to compile: %w", bytes, err)
		}
		*held += bytes
		return nil
	})
	if errors.Is(err, ecma262.ErrLimit) {
		// Past one of goecma262's limits, which JavaScript does not have:
		// an engine limit, as in expressions.
		return nil, fmt.Errorf("countpattern: %w: %w", ErrLimit, err)
	}
	if err != nil {
		return nil, err
	}
	if err := led.Work(int64(len(source) + p.Re.ProgramSize())); err != nil {
		return nil, err
	}
	return p, nil
}

// searchFailed is the error of a search that failed: out of goecma262's step
// or memory budget, an engine limit (ErrLimit), as in expressions.
func searchFailed(err error) error {
	if errors.Is(err, ecma262.ErrStepLimit) {
		return fmt.Errorf("countpattern: %w: %w", ErrLimit, err)
	}
	return fmt.Errorf("countpattern: %w", err)
}
