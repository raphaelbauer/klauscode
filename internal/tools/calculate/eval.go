package calculate

import (
	"fmt"
	"strconv"
	"unicode"
)

// Eval evaluates a basic arithmetic expression and returns the result.
//
// It supports decimal numbers, the binary operators + - * /, unary minus and
// plus, and parentheses, with standard precedence (* and / bind tighter than
// + and -). It is a small hand-written recursive-descent parser so the harness
// stays dependency-free.
//
// Grammar:
//
//	expr   = term   { ("+" | "-") term }
//	term   = factor { ("*" | "/") factor }
//	factor = number | "(" expr ")" | ("+" | "-") factor
func Eval(expr string) (float64, error) {
	p := &parser{input: expr}
	if err := p.next(); err != nil { // prime the first token
		return 0, err
	}
	value, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	if p.tok.kind != tokEOF {
		return 0, fmt.Errorf("unexpected trailing input %q", p.tok.text)
	}
	return value, nil
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
	num  float64
}

// parser is a single-pass recursive-descent parser over input.
type parser struct {
	input string
	pos   int
	tok   token
}

// next scans the next token into p.tok.
func (p *parser) next() error {
	// skip whitespace
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
	if p.pos >= len(p.input) {
		p.tok = token{kind: tokEOF}
		return nil
	}

	ch := p.input[p.pos]
	switch ch {
	case '+':
		p.pos++
		p.tok = token{kind: tokPlus, text: "+"}
	case '-':
		p.pos++
		p.tok = token{kind: tokMinus, text: "-"}
	case '*':
		p.pos++
		p.tok = token{kind: tokStar, text: "*"}
	case '/':
		p.pos++
		p.tok = token{kind: tokSlash, text: "/"}
	case '(':
		p.pos++
		p.tok = token{kind: tokLParen, text: "("}
	case ')':
		p.pos++
		p.tok = token{kind: tokRParen, text: ")"}
	default:
		if isNumberStart(ch) {
			return p.scanNumber()
		}
		return fmt.Errorf("unexpected character %q", string(ch))
	}
	return nil
}

func isNumberStart(ch byte) bool {
	return (ch >= '0' && ch <= '9') || ch == '.'
}

// scanNumber consumes a decimal number starting at p.pos.
func (p *parser) scanNumber() error {
	start := p.pos
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			p.pos++
			continue
		}
		break
	}
	text := p.input[start:p.pos]
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("invalid number %q", text)
	}
	p.tok = token{kind: tokNumber, text: text, num: value}
	return nil
}

func (p *parser) parseExpr() (float64, error) {
	value, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for p.tok.kind == tokPlus || p.tok.kind == tokMinus {
		op := p.tok.kind
		if err := p.next(); err != nil {
			return 0, err
		}
		rhs, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == tokPlus {
			value += rhs
		} else {
			value -= rhs
		}
	}
	return value, nil
}

func (p *parser) parseTerm() (float64, error) {
	value, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for p.tok.kind == tokStar || p.tok.kind == tokSlash {
		op := p.tok.kind
		if err := p.next(); err != nil {
			return 0, err
		}
		rhs, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == tokStar {
			value *= rhs
		} else {
			if rhs == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			value /= rhs
		}
	}
	return value, nil
}

func (p *parser) parseFactor() (float64, error) {
	switch p.tok.kind {
	case tokNumber:
		value := p.tok.num
		if err := p.next(); err != nil {
			return 0, err
		}
		return value, nil
	case tokPlus, tokMinus:
		op := p.tok.kind
		if err := p.next(); err != nil {
			return 0, err
		}
		value, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == tokMinus {
			return -value, nil
		}
		return value, nil
	case tokLParen:
		if err := p.next(); err != nil {
			return 0, err
		}
		value, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.tok.kind != tokRParen {
			return 0, fmt.Errorf("expected closing parenthesis")
		}
		if err := p.next(); err != nil {
			return 0, err
		}
		return value, nil
	case tokEOF:
		return 0, fmt.Errorf("unexpected end of expression")
	default:
		return 0, fmt.Errorf("unexpected token %q", p.tok.text)
	}
}
