// Package rules — condition parser + evaluator.
//
// The condition DSL is intentionally tiny:
//
//   primitive    := changed(glob)
//                 | any_change(glob)
//                 | commit_message_contains(s)
//                 | tool_call_count(name) > N
//                 | arg(key) == value
//                 | docsync_violation(unused)   // true iff ctx.DocsyncViolations non-empty (caller-precomputed)
//                 | guardians_check(plan_arg)   // phase-1 stub, always true
//                 | true | false
//   expression   := primitive | NOT expression | expression AND expression | expression OR expression
//
// Operators are case-insensitive (`AND`, `and`, `&&` all work).
// Parens group; precedence is NOT > AND > OR.
//
// We don't ship a full PEG parser — the grammar fits on one screen
// of recursive-descent. Adding clauses (`pred OR pred`) is one new
// case in parseOr; adding predicates is one entry in callPredicate.
//
// Anti-pattern guard: the DSL is deliberately read-only on
// Context. No predicate spawns a process, opens a file, or hits
// the network. If a future rule needs that, the caller pre-loads
// the data into Context fields BEFORE calling Evaluate. This
// keeps Evaluate pure / fast / deterministic.

package rules

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Evaluate runs every rule whose When matches ctx.Event against the
// context. Rules are evaluated in declaration order. A condition
// parse failure surfaces as a Result with Passed=false, Reason
// naming the parse error, Severity propagated from the rule —
// otherwise a typo in TOML would silently skip the rule.
func Evaluate(rules []Rule, ctx Context) Verdict {
	out := Verdict{Event: ctx.Event}
	for _, r := range rules {
		if r.Severity == SeverityOff {
			continue
		}
		if r.When != ctx.Event {
			continue
		}
		res := evalRule(r, ctx)
		out.Results = append(out.Results, res)
		if !res.Passed {
			switch res.Severity {
			case SeverityBlock:
				out.Blocked = append(out.Blocked, res)
			case SeverityWarn:
				out.Warnings = append(out.Warnings, res)
			}
		}
	}
	return out
}

func evalRule(r Rule, ctx Context) Result {
	// Lazy parse: if the loader already populated r.parsed, reuse;
	// otherwise parse here. Tests construct rules ad-hoc and
	// don't call the loader, so this fall-through keeps them
	// terse.
	parsed := r.parsed
	if parsed == nil {
		p, err := parseExpr(r.Condition)
		if err != nil {
			return Result{
				Rule:     r.Name,
				Severity: r.Severity,
				Passed:   false,
				Reason:   fmt.Sprintf("condition parse error: %v", err),
				Hint:     r.Hint,
			}
		}
		parsed = p
	}
	ok, why, err := parsed.eval(ctx)
	if err != nil {
		return Result{Rule: r.Name, Severity: r.Severity, Passed: false,
			Reason: fmt.Sprintf("evaluator error: %v", err), Hint: r.Hint}
	}
	if ok {
		return Result{Rule: r.Name, Severity: r.Severity, Passed: true}
	}
	return Result{Rule: r.Name, Severity: r.Severity, Passed: false,
		Reason: why, Hint: r.Hint}
}

// ─── AST ──────────────────────────────────────────────────────────

// expr is the parsed condition AST node. eval returns
// (matched, why-not, err): when matched=true, why-not is empty;
// when matched=false, why-not is a human-readable failure reason.
type expr interface {
	eval(ctx Context) (matched bool, whyNot string, err error)
}

type litExpr struct{ v bool }

func (l litExpr) eval(_ Context) (bool, string, error) { return l.v, "", nil }

type notExpr struct{ inner expr }

func (n notExpr) eval(c Context) (bool, string, error) {
	ok, _, err := n.inner.eval(c)
	if err != nil {
		return false, "", err
	}
	if ok {
		return false, "negation: inner expression matched", nil
	}
	return true, "", nil
}

type andExpr struct{ left, right expr }

func (a andExpr) eval(c Context) (bool, string, error) {
	ok, why, err := a.left.eval(c)
	if err != nil {
		return false, "", err
	}
	if !ok {
		return false, why, nil
	}
	return a.right.eval(c)
}

type orExpr struct{ left, right expr }

func (o orExpr) eval(c Context) (bool, string, error) {
	ok, _, err := o.left.eval(c)
	if err != nil {
		return false, "", err
	}
	if ok {
		return true, "", nil
	}
	return o.right.eval(c)
}

// callExpr is one predicate invocation: name(arg) [op N].
type callExpr struct {
	name string
	arg  string
	cmp  string // "" | ">" | ">=" | "==" | "!="
	num  int
	rhs  string // for "==" / "!=" string compare
}

func (c callExpr) eval(ctx Context) (bool, string, error) {
	switch c.name {
	case "changed", "any_change":
		// changed(glob) → true iff any path in ChangedPaths
		// matches glob. any_change is an alias.
		for _, p := range ctx.ChangedPaths {
			match, _ := doublestar.PathMatch(c.arg, p)
			if match {
				return true, "", nil
			}
		}
		return false, fmt.Sprintf("no changed path matched %q", c.arg), nil

	case "commit_message_contains":
		if strings.Contains(ctx.CommitMessage, c.arg) {
			return true, "", nil
		}
		return false, fmt.Sprintf("commit message does not contain %q", c.arg), nil

	case "tool_call_count":
		count := ctx.ToolCalls[c.arg]
		switch c.cmp {
		case ">":
			if count > c.num {
				return true, "", nil
			}
		case ">=":
			if count >= c.num {
				return true, "", nil
			}
		case "==":
			if count == c.num {
				return true, "", nil
			}
		case "!=":
			if count != c.num {
				return true, "", nil
			}
		default:
			return false, "", fmt.Errorf("tool_call_count needs a comparison (>, >=, ==, !=)")
		}
		return false, fmt.Sprintf("tool_call_count(%s) = %d, want %s %d",
			c.arg, count, c.cmp, c.num), nil

	case "arg":
		v := ctx.Args[c.arg]
		switch c.cmp {
		case "==":
			if v == c.rhs {
				return true, "", nil
			}
			return false, fmt.Sprintf("arg(%s) = %q, want == %q", c.arg, v, c.rhs), nil
		case "!=":
			if v != c.rhs {
				return true, "", nil
			}
			return false, fmt.Sprintf("arg(%s) = %q, want != %q", c.arg, v, c.rhs), nil
		case "~~":
			// Glob match (doublestar) — RHS is a pattern, LHS the
			// arg value. Used by the pre_push branch-protection
			// rule to express `branch ~~ "release/*"` without
			// needing to OR every release branch literal.
			match, err := doublestar.PathMatch(c.rhs, v)
			if err != nil {
				return false, "", fmt.Errorf("arg(%s) ~~ %q: bad glob: %w", c.arg, c.rhs, err)
			}
			if match {
				return true, "", nil
			}
			return false, fmt.Sprintf("arg(%s) = %q, want ~~ %q", c.arg, v, c.rhs), nil
		case "!~":
			// Negated glob match — convenience inverse of ~~.
			match, err := doublestar.PathMatch(c.rhs, v)
			if err != nil {
				return false, "", fmt.Errorf("arg(%s) !~ %q: bad glob: %w", c.arg, c.rhs, err)
			}
			if !match {
				return true, "", nil
			}
			return false, fmt.Sprintf("arg(%s) = %q, want !~ %q", c.arg, v, c.rhs), nil
		case "^=":
			// Prefix match — used by the pre_push wip-on-protected
			// rule to test `arg("head_subject") ^= "wip!:"` without
			// the false-positive surface of commit_message_contains
			// (which would match "wip!:" appearing anywhere in the
			// body, not just the subject prefix).
			if strings.HasPrefix(v, c.rhs) {
				return true, "", nil
			}
			return false, fmt.Sprintf("arg(%s) = %q, want ^= %q", c.arg, v, c.rhs), nil
		default:
			return false, "", fmt.Errorf("arg() needs ==, !=, ~~, !~, or ^= comparison")
		}

	case "docsync_violation":
		// Pre-computed by the caller (runCommit / runRulesCheck)
		// via internal/checkpoint.CheckDocsync. The arg is
		// ignored — kept in the AST shape so the parser's
		// `ident "(" arg ")"` rule still matches; a 0-arg
		// spelling (`docsync_violation()`) would require a
		// second parser branch and isn't worth the complexity.
		// Passing `"go"` (any string) is the recommended
		// canonical form.
		_ = c.arg
		if len(ctx.DocsyncViolations) > 0 {
			return true, "", nil
		}
		return false, "no docsync violations (every changed *.go has its sibling *.md updated, or no sibling exists)", nil

	case "guardians_check":
		// Phase-1 STUB for metareflection/guardians (MIT,
		// https://github.com/metareflection/guardians) — a
		// taint-tracking + Z3-SAT plan-level pre_send predicate.
		// Today this always returns true (never blocks); the
		// surface contract exists so operators can wire
		// `guardians_check("plan")` into their pre_send rules
		// now and have the verdict flip to a real Z3 result
		// once phase-2 lands the engine behind a build tag.
		//
		// The arg names a Context.Args key that holds the
		// drafted plan / message body. Phase-2 will: read
		// ctx.Args[c.arg], run the taint propagation pass over
		// the plan's tool-call graph, encode the safety
		// invariants as Z3 assertions, and return false +
		// reason on UNSAT.
		_ = ctx.Args[c.arg] // touch the arg so the contract stays explicit
		return true, "", nil
	}
	return false, "", fmt.Errorf("unknown predicate %q", c.name)
}

// ─── parser ───────────────────────────────────────────────────────

// parseExpr is the public entry; tokens are produced by tokenize.
func parseExpr(src string) (expr, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty condition")
	}
	p := &parser{toks: toks}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("trailing tokens after expression: %v", p.toks[p.pos:])
	}
	return e, nil
}

type token struct {
	kind  string // "ident", "string", "number", "(", ")", "and", "or", "not", "op", "comma"
	value string
}

func tokenize(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(' || c == ')' || c == ',':
			out = append(out, token{kind: string(c), value: string(c)})
			i++
		case c == '"' || c == '\'':
			quote := c
			j := i + 1
			for j < len(src) && src[j] != quote {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string at offset %d", i)
			}
			out = append(out, token{kind: "string", value: src[i+1 : j]})
			i = j + 1
		case c == '>' || c == '<' || c == '=' || c == '!':
			// Two-char ops first: >=, <=, ==, !=, !~ (negated glob).
			if i+1 < len(src) && (src[i+1] == '=') {
				out = append(out, token{kind: "op", value: src[i : i+2]})
				i += 2
			} else if c == '!' && i+1 < len(src) && src[i+1] == '~' {
				out = append(out, token{kind: "op", value: "!~"})
				i += 2
			} else if c == '>' || c == '<' {
				out = append(out, token{kind: "op", value: string(c)})
				i++
			} else {
				return nil, fmt.Errorf("stray %q at offset %d", c, i)
			}
		case c == '~' && i+1 < len(src) && src[i+1] == '~':
			// `~~` glob-match operator (used by arg(key) ~~ "release/*").
			out = append(out, token{kind: "op", value: "~~"})
			i += 2
		case c == '^' && i+1 < len(src) && src[i+1] == '=':
			// `^=` prefix-match operator (arg(key) ^= "wip!:").
			out = append(out, token{kind: "op", value: "^="})
			i += 2
		case c == '&' && i+1 < len(src) && src[i+1] == '&':
			out = append(out, token{kind: "and", value: "&&"})
			i += 2
		case c == '|' && i+1 < len(src) && src[i+1] == '|':
			out = append(out, token{kind: "or", value: "||"})
			i += 2
		case isDigit(c) || (c == '-' && i+1 < len(src) && isDigit(src[i+1])):
			j := i
			if c == '-' {
				j++
			}
			for j < len(src) && isDigit(src[j]) {
				j++
			}
			out = append(out, token{kind: "number", value: src[i:j]})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentBody(src[j]) {
				j++
			}
			word := src[i:j]
			lower := strings.ToLower(word)
			switch lower {
			case "and":
				out = append(out, token{kind: "and", value: word})
			case "or":
				out = append(out, token{kind: "or", value: word})
			case "not":
				out = append(out, token{kind: "not", value: word})
			case "true", "false":
				out = append(out, token{kind: "bool", value: lower})
			default:
				out = append(out, token{kind: "ident", value: word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected %q at offset %d", c, i)
		}
	}
	return out, nil
}

func isDigit(b byte) bool      { return b >= '0' && b <= '9' }
func isIdentStart(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' }
func isIdentBody(b byte) bool  { return isIdentStart(b) || isDigit(b) }

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() *token {
	if p.pos >= len(p.toks) {
		return nil
	}
	return &p.toks[p.pos]
}

func (p *parser) advance() *token {
	if p.pos >= len(p.toks) {
		return nil
	}
	t := &p.toks[p.pos]
	p.pos++
	return t
}

// parseOr is the lowest-precedence rung.
func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "or" {
			return left, nil
		}
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orExpr{left: left, right: right}
	}
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "and" {
			return left, nil
		}
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andExpr{left: left, right: right}
	}
}

func (p *parser) parseNot() (expr, error) {
	if t := p.peek(); t != nil && t.kind == "not" {
		p.advance()
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notExpr{inner: inner}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (expr, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	switch t.kind {
	case "(":
		p.advance()
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		closing := p.advance()
		if closing == nil || closing.kind != ")" {
			return nil, fmt.Errorf("missing closing paren")
		}
		return e, nil
	case "bool":
		p.advance()
		return litExpr{v: t.value == "true"}, nil
	case "ident":
		return p.parseCall()
	}
	return nil, fmt.Errorf("unexpected token %q", t.value)
}

// parseCall expects: ident "(" arg ")" [op rhs].
func (p *parser) parseCall() (expr, error) {
	name := p.advance().value
	open := p.advance()
	if open == nil || open.kind != "(" {
		return nil, fmt.Errorf("expected '(' after %s", name)
	}
	argTok := p.advance()
	if argTok == nil {
		return nil, fmt.Errorf("expected argument after %s(", name)
	}
	arg := argTok.value
	if argTok.kind != "string" && argTok.kind != "ident" {
		return nil, fmt.Errorf("%s: expected string or identifier arg, got %q", name, argTok.value)
	}
	closing := p.advance()
	if closing == nil || closing.kind != ")" {
		return nil, fmt.Errorf("missing ')' after %s arg", name)
	}
	out := callExpr{name: name, arg: arg}

	// Optional comparison after the call.
	if t := p.peek(); t != nil && t.kind == "op" {
		op := p.advance().value
		rhsTok := p.advance()
		if rhsTok == nil {
			return nil, fmt.Errorf("expected RHS after %s", op)
		}
		out.cmp = op
		switch rhsTok.kind {
		case "number":
			n, err := strconv.Atoi(rhsTok.value)
			if err != nil {
				return nil, fmt.Errorf("bad number %q: %w", rhsTok.value, err)
			}
			out.num = n
		case "string":
			out.rhs = rhsTok.value
		default:
			return nil, fmt.Errorf("unexpected rhs token %q", rhsTok.value)
		}
	}
	return out, nil
}
