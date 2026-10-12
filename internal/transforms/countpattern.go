package transforms

import (
	"context"
	"fmt"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
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
	// The two programs are the call's while it runs, paid for before they
	// are compiled (see payPattern).
	led := budget.LedgerFrom(ctx)
	var held int64
	defer func() { led.Free(held) }()
	match, err := jsval.NewPattern(pattern, "g", payPattern(ctx, led, &held))
	if err != nil {
		return nil, err
	}
	stop, err := jsval.NewPattern("^"+p.Stopwords+"$", "i", payPattern(ctx, led, &held))
	if err != nil {
		return nil, err
	}
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
		ms, err := match.Re.FindAllStringSubmatchIndexErr(text, -1)
		if err != nil {
			return nil, fmt.Errorf("countpattern: %w", err)
		}
		for _, m := range ms {
			tok := text[m[0]:m[1]]
			isStop, err := stop.Re.MatchStringErr(tok)
			if err != nil {
				return nil, fmt.Errorf("countpattern: %w", err)
			}
			if isStop {
				continue
			}
			if _, ok := counts[tok]; !ok {
				// Each distinct token is an output row: reserve them as they
				// are found, a thousand at a time.
				if len(order)&1023 == 0 {
					if err := reserveOut(ctx, len(order)+1024, len(data)); err != nil {
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

// payPattern pays for a pattern's compile with the ledger, adding what it
// holds to *held: goecma262 cannot be interrupted while it compiles, so the
// program is charged from its bound and the deadline polled before the
// compile starts. A program past the ledger is refused uncompiled.
func payPattern(ctx context.Context, led *budget.Ledger, held *int64) jsval.PatternPay {
	return func(bytes, work int64) error {
		if err := led.Work(work); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if bytes < 0 {
			led.Free(-bytes)
			*held += bytes
			return nil
		}
		if err := led.Alloc(bytes); err != nil {
			return fmt.Errorf("countpattern: a regular expression compiles to a program of %d bytes: %w", bytes, err)
		}
		*held += bytes
		return nil
	}
}
