package model

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MerchantStoreSVGMaxBytes = 128 << 10
const MerchantStoreSVGDataPrefix = "data:image/svg+xml;base64,"
const storeSVGNamespace = "http://www.w3.org/2000/svg"

// NormalizeMerchantStoreImage validates a standalone visual image. It is also
// used by merchant profiles; ordinary links keep their HTTP(S) contract.
func NormalizeMerchantStoreImage(value string) (string, error) {
	return normalizeMerchantStoreImage(value)
}

// Keep product media in the existing ordered image_urls array.
func normalizeMerchantStoreImage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if storeURL(value) {
		return value, nil
	}
	source := value
	if strings.HasPrefix(value, MerchantStoreSVGDataPrefix) {
		encoded := strings.TrimPrefix(value, MerchantStoreSVGDataPrefix)
		if len(encoded) > (MerchantStoreSVGMaxBytes+2)/3*4 || strings.ContainsAny(encoded, "\r\n\t ") {
			return "", ErrMerchantStoreInput
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return "", ErrMerchantStoreInput
		}
		source = string(decoded)
	}
	if len(source) > MerchantStoreSVGMaxBytes || !utf8.ValidString(source) {
		return "", ErrMerchantStoreInput
	}
	source, err := validateMerchantStoreSVG(source)
	if err != nil {
		return "", err
	}
	return MerchantStoreSVGDataPrefix + base64.StdEncoding.EncodeToString([]byte(source)), nil
}

var storeSVGTags = storeSVGSet("svg g defs path rect circle ellipse line polyline polygon text tspan title desc linearGradient radialGradient stop clipPath animate animateTransform")
var storeSVGAttrs = storeSVGSet("id viewBox width height x y x1 y1 x2 y2 cx cy r rx ry d points transform fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset opacity clip-path clip-rule clipPathUnits gradientUnits gradientTransform spreadMethod offset stop-color stop-opacity color font-size font-family font-weight font-style text-anchor dominant-baseline letter-spacing preserveAspectRatio aria-label aria-labelledby role")
var storeSVGLocalPaint = regexp.MustCompile(`^url\(#[A-Za-z_][A-Za-z0-9_.:-]*\)$`)
var storeSVGURLFunction = regexp.MustCompile(`(?i)url\s*\(`)
var storeSVGNumber = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

func storeSVGSet(words string) map[string]bool {
	result := map[string]bool{}
	for _, word := range strings.Fields(words) {
		result[word] = true
	}
	return result
}

func storeSVGValueSafe(value string) bool {
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, "\\") || strings.Contains(lower, "://") || strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") || strings.Contains(lower, "vbscript:") || strings.Contains(lower, "@import") || strings.Contains(lower, "/*") {
		return false
	}
	return !storeSVGURLFunction.MatchString(value) || storeSVGLocalPaint.MatchString(value)
}

func storeSVGDimension(value string) bool {
	value = strings.TrimSuffix(strings.TrimSpace(value), "px")
	limit := 4096.0
	if strings.HasSuffix(value, "%") {
		value = strings.TrimSuffix(value, "%")
		limit = 100
	}
	if !storeSVGNumber.MatchString(value) {
		return false
	}
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) && number > 0 && number <= limit
}

func storeSVGViewBox(value string) bool {
	parts := strings.Fields(strings.ReplaceAll(value, ",", " "))
	if len(parts) != 4 {
		return false
	}
	for index, part := range parts {
		if !storeSVGNumber.MatchString(part) {
			return false
		}
		number, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > 4096 || (index >= 2 && number <= 0) {
			return false
		}
	}
	return true
}

func validateMerchantStoreSVG(source string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(source))
	depth, nodes := 0, 0
	root, closed, namespace := false, false, false
	rootOffset := 0
	var parents []string
	for {
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", ErrMerchantStoreInput
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			nodes++
			if closed || depth > 32 || nodes > 4096 || len(token.Attr) > 64 || !storeSVGTags[token.Name.Local] || (token.Name.Space != "" && token.Name.Space != storeSVGNamespace) {
				return "", ErrMerchantStoreInput
			}
			if !root {
				if token.Name.Local != "svg" {
					return "", ErrMerchantStoreInput
				}
				root, rootOffset = true, int(offset)
			} else if token.Name.Local == "svg" {
				return "", ErrMerchantStoreInput
			}
			parent := ""
			if len(parents) > 0 {
				parent = parents[len(parents)-1]
			}
			if storeSVGAnimationTag(parent) {
				return "", ErrMerchantStoreInput
			}
			animation := storeSVGAnimationTag(token.Name.Local)
			seen := map[xml.Name]bool{}
			for _, attr := range token.Attr {
				if seen[attr.Name] {
					return "", ErrMerchantStoreInput
				}
				seen[attr.Name] = true
				if attr.Name.Local == "xmlns" && attr.Name.Space == "" && depth == 1 && attr.Value == storeSVGNamespace {
					namespace = true
					continue
				}
				if attr.Name.Space != "" || !storeSVGValueSafe(attr.Value) || (!animation && !storeSVGAttrs[attr.Name.Local]) {
					return "", ErrMerchantStoreInput
				}
				if depth == 1 && ((attr.Name.Local == "width" || attr.Name.Local == "height") && !storeSVGDimension(attr.Value) || attr.Name.Local == "viewBox" && !storeSVGViewBox(attr.Value)) {
					return "", ErrMerchantStoreInput
				}
			}
			if animation && !storeSVGAnimationSafe(token, parent) {
				return "", ErrMerchantStoreInput
			}
			parents = append(parents, token.Name.Local)
		case xml.EndElement:
			depth--
			parents = parents[:len(parents)-1]
			if depth == 0 {
				closed = true
			}
		case xml.CharData:
			if (depth == 0 || len(parents) > 0 && storeSVGAnimationTag(parents[len(parents)-1])) && strings.TrimSpace(string(token)) != "" {
				return "", ErrMerchantStoreInput
			}
		case xml.Directive, xml.ProcInst:
			return "", ErrMerchantStoreInput
		}
	}
	if !root || !closed || depth != 0 {
		return "", ErrMerchantStoreInput
	}
	if !namespace {
		// InputOffset points to the actual root, never an svg inside a comment.
		start := rootOffset + strings.Index(source[rootOffset:], "<svg") + len("<svg")
		if start < rootOffset+len("<svg") {
			return "", ErrMerchantStoreInput
		}
		source = source[:start] + ` xmlns="` + storeSVGNamespace + `"` + source[start:]
	}
	if len(source) > MerchantStoreSVGMaxBytes {
		return "", ErrMerchantStoreInput
	}
	return source, nil
}
