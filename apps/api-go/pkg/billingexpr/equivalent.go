package billingexpr

import (
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// PricingEquivalent proves equivalence of common billing formulas without
// executing them on sample requests. It compares exact rational coefficients,
// preserves conditional boundaries, and ignores tier labels (audit metadata).
// Unsupported syntax remains a difference. This never rewrites stored rules.
func PricingEquivalent(left, right string) bool {
	if left == right {
		return true
	}
	lv, lb := ParseExprVersion(strings.TrimSpace(left))
	rv, rb := ParseExprVersion(strings.TrimSpace(right))
	if lv != rv || len(lb) > 64*1024 || len(rb) > 64*1024 {
		return false
	}
	lt, le := parser.Parse(lb)
	rt, re := parser.Parse(rb)
	if le != nil || re != nil {
		return false
	}
	// Referencing a category changes how p/c are normalized, even when its
	// coefficient is zero. Optional measurements must also remain required.
	if normalizationSignature(lt.Node) != normalizationSignature(rt.Node) {
		return false
	}
	lc, lok := pricePolynomial(lt.Node, 0)
	rc, rok := pricePolynomial(rt.Node, 0)
	return lok && rok && polynomialKey(lc) == polynomialKey(rc)
}

var normalizationDimensions = map[string]bool{
	"cr": true, "cc": true, "cc1h": true, "img": true, "img_o": true,
	"ai": true, "ao": true, "cr_text": true, "cr_img": true,
	"cr_audio": true, "audio_s": true,
}

func normalizationSignature(node ast.Node) string {
	used := map[string]bool{}
	ast.Find(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.IdentifierNode); ok && normalizationDimensions[id.Value] {
			used[id.Value] = true
		}
		return false
	})
	keys := make([]string, 0, len(used))
	for key := range used {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// BillingConditions returns the existing decision/probe footprint, independent
// of monetary coefficients and tier labels. Sync may update rates, but must not
// silently drop/change a threshold, timezone, parameter or calendar condition.
func BillingConditions(expression string) (string, bool) {
	_, body := ParseExprVersion(strings.TrimSpace(expression))
	if len(body) > 64*1024 {
		return "", false
	}
	tree, err := parser.Parse(body)
	if err != nil {
		return "", false
	}
	conditions := []string{}
	ast.Find(tree.Node, func(n ast.Node) bool {
		if choice, ok := n.(*ast.ConditionalNode); ok {
			conditions = append(conditions, "condition:"+choice.Cond.String())
		}
		if call, ok := n.(*ast.CallNode); ok {
			if name, ok := call.Callee.(*ast.IdentifierNode); ok {
				switch name.Value {
				case "param", "header", "has", "hour", "minute", "weekday", "month", "day", "date":
					conditions = append(conditions, "probe:"+call.String())
				}
			}
		}
		return false
	})
	sort.Strings(conditions)
	encoded, _ := json.Marshal(conditions)
	return string(encoded), true
}

// A polynomial is a sum of exact numeric coefficients times structural atoms.
// Only multiplication/division by constants is simplified. Nonlinear terms and
// functions retain their exact AST, avoiding unsafe cancellation or sampling.
type priceTerms map[string]*big.Rat

func pricePolynomial(node ast.Node, depth int) (priceTerms, bool) {
	if depth > 128 {
		return nil, false
	}
	constant := func(value string) (priceTerms, bool) {
		r, ok := new(big.Rat).SetString(value)
		if !ok {
			return nil, false
		}
		f, _ := r.Float64()
		// Extreme floating-point intermediates can overflow/underflow before
		// algebraic cancellation. Only ordinary billing-sized numbers receive
		// symbolic simplification; extreme rules remain explicit differences.
		if math.IsInf(f, 0) || (r.Sign() != 0 && (math.Abs(f) > 1e100 || math.Abs(f) < 1e-100)) {
			return nil, false
		}
		return priceTerms{"": r}, true
	}
	switch n := node.(type) {
	case *ast.IntegerNode:
		return constant(strconv.Itoa(n.Value))
	case *ast.FloatNode:
		return constant(strconv.FormatFloat(n.Value, 'g', -1, 64))
	case *ast.IdentifierNode:
		return priceTerms{"var:" + n.Value: big.NewRat(1, 1)}, true
	case *ast.UnaryNode:
		terms, ok := pricePolynomial(n.Node, depth+1)
		if !ok || (n.Operator != "+" && n.Operator != "-") {
			return nil, false
		}
		if n.Operator == "-" {
			return scaleTerms(terms, big.NewRat(-1, 1)), true
		}
		return terms, true
	case *ast.BinaryNode:
		left, lok := pricePolynomial(n.Left, depth+1)
		right, rok := pricePolynomial(n.Right, depth+1)
		if !lok || !rok {
			return nil, false
		}
		switch n.Operator {
		case "+", "-":
			if n.Operator == "-" {
				right = scaleTerms(right, big.NewRat(-1, 1))
			}
			for key, rate := range right {
				if previous, ok := left[key]; ok {
					left[key] = new(big.Rat).Add(previous, rate)
				} else {
					left[key] = new(big.Rat).Set(rate)
				}
			}
			return left, true
		case "*":
			if scalar, ok := constantTerm(left); ok {
				return scaleTerms(right, scalar), true
			}
			if scalar, ok := constantTerm(right); ok {
				return scaleTerms(left, scalar), true
			}
		case "/":
			if scalar, ok := constantTerm(right); ok && scalar.Sign() != 0 {
				return scaleTerms(left, new(big.Rat).Inv(scalar)), true
			}
			// Never cancel variable denominators or division by zero.
			return nil, false
		default:
			return nil, false
		}
		atom, _ := json.Marshal([]string{n.Operator, polynomialKey(left), polynomialKey(right)})
		return priceTerms{string(atom): big.NewRat(1, 1)}, true
	case *ast.CallNode:
		name, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return nil, false
		}
		if name.Value == "tier" {
			if len(n.Arguments) != 2 {
				return nil, false
			}
			if _, literalLabel := n.Arguments[0].(*ast.StringNode); !literalLabel {
				return nil, false
			}
			return pricePolynomial(n.Arguments[1], depth+1)
		}
		// Retain function argument types, order and spelling. In particular,
		// changing a timezone or rounding function is not a price equivalence.
		return priceTerms{"call:" + n.String(): big.NewRat(1, 1)}, true
	case *ast.ConditionalNode:
		left, lok := pricePolynomial(n.Exp1, depth+1)
		right, rok := pricePolynomial(n.Exp2, depth+1)
		if !lok || !rok {
			return nil, false
		}
		atom, _ := json.Marshal([]string{"if", n.Cond.String(), polynomialKey(left), polynomialKey(right)})
		return priceTerms{string(atom): big.NewRat(1, 1)}, true
	default:
		return nil, false
	}
}

func constantTerm(terms priceTerms) (*big.Rat, bool) {
	if len(terms) == 1 {
		value, ok := terms[""]
		return value, ok
	}
	return nil, false
}

func scaleTerms(terms priceTerms, factor *big.Rat) priceTerms {
	for key, value := range terms {
		terms[key] = new(big.Rat).Mul(value, factor)
	}
	return terms
}

func polynomialKey(terms priceTerms) string {
	keys := make([]string, 0, len(terms))
	for key := range terms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([][2]string, 0, len(keys))
	for _, key := range keys {
		if key == "" && terms[key].Sign() == 0 {
			continue // An additive literal zero has no evaluation side effects.
		}
		// Keep zero-coefficient atoms: they may require a measured dimension
		// or evaluate an otherwise invalid operation. Zero is not a wildcard.
		items = append(items, [2]string{key, terms[key].RatString()})
	}
	encoded, _ := json.Marshal(items)
	return string(encoded)
}
