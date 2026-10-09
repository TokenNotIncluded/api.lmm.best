package servicetier

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxPricingBytes = 2 << 20
const pricingHeader = "| Model | Short context input | Short context cached input | Short context cache writes | Short context output | Long context input | Long context cached input | Long context cache writes | Long context output |"

var thresholdPattern = regexp.MustCompile(`Short context: ≤([0-9]+)K input tokens\. Long context: >([0-9]+)K input tokens\.`)
var modelNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*$`)

// FetchCatalog has a fixed official destination, sends no keys and publishes no
// partial results. A changed document format is an error, never a free price.
func FetchCatalog(ctx context.Context) (Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PricingURL, nil)
	if err != nil {
		return Catalog{}, err
	}
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("User-Agent", "LMM-ServiceTier-Pricing/1")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return Catalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("official pricing returned HTTP %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/markdown") && !strings.HasPrefix(contentType, "text/plain") {
		return Catalog{}, errors.New("official pricing did not return Markdown")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPricingBytes+1))
	if err != nil {
		return Catalog{}, err
	}
	if len(data) > maxPricingBytes {
		return Catalog{}, errors.New("official pricing document is too large")
	}
	return ParseCatalog(string(data), time.Now().UTC())
}
func parsePrice(text string) (*float64, error) {
	if text == "-" {
		return nil, nil
	}
	if !strings.HasPrefix(text, "$") {
		return nil, errors.New("price is not explicitly USD")
	}
	n, err := strconv.ParseFloat(strings.TrimPrefix(text, "$"), 64)
	if err != nil || !finite(n) || n < 0 {
		return nil, errors.New("invalid official price")
	}
	return &n, nil
}
func parseRates(cells []string) (*Rates, error) {
	values := make([]*float64, 4)
	for i, c := range cells {
		var err error
		values[i], err = parsePrice(c)
		if err != nil {
			return nil, err
		}
	}
	if values[0] == nil && values[1] == nil && values[2] == nil && values[3] == nil {
		return nil, nil
	}
	if values[0] == nil || values[3] == nil {
		return nil, errors.New("partial input/output pricing row")
	}
	r := &Rates{Input: *values[0], CachedInput: values[1], CacheWrite: values[2], Output: *values[3]}
	return r, r.validate()
}
func ParseCatalog(document string, now time.Time) (Catalog, error) {
	if len(document) > maxPricingBytes {
		return Catalog{}, errors.New("pricing document too large")
	}
	thresholds := thresholdPattern.FindAllStringSubmatch(document, -1)
	if len(thresholds) != 1 || thresholds[0][1] != thresholds[0][2] {
		return Catalog{}, errors.New("unrecognized official context threshold")
	}
	limit, err := strconv.Atoi(thresholds[0][1])
	if err != nil || limit < 1 || limit > 10000 {
		return Catalog{}, errors.New("invalid context threshold")
	}
	limit *= 1000
	tables := map[string]map[string]TierRates{}
	scanner := bufio.NewScanner(strings.NewReader(document))
	scanner.Buffer(make([]byte, 4096), maxPricingBytes)
	active := ""
	headerSeen := false
	rowSeen := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "### ") {
			active = ""
			headerSeen = false
			rowSeen = false
			switch line {
			case "### Standard pricing data":
				active = "default"
			case "### Fast pricing data":
				active = "fast"
			case "### Ultrafast pricing data":
				active = "ultrafast"
			}
			if active != "" {
				if _, ok := tables[active]; ok {
					return Catalog{}, errors.New("duplicate official pricing section")
				}
				tables[active] = map[string]TierRates{}
			}
			continue
		}
		if active == "" {
			continue
		}
		if line == "" {
			if rowSeen {
				active = ""
			}
			continue
		}
		if !headerSeen {
			if line != pricingHeader {
				return Catalog{}, errors.New("official pricing columns changed")
			}
			headerSeen = true
			continue
		}
		if strings.HasPrefix(line, "| ---") {
			continue
		}
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			return Catalog{}, errors.New("malformed official pricing table")
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) != 9 {
			return Catalog{}, errors.New("incomplete official pricing row")
		}
		name := strings.TrimSuffix(cells[0], fmt.Sprintf(" (<%dK context length)", limit/1000))
		if !modelNamePattern.MatchString(name) {
			return Catalog{}, fmt.Errorf("unrecognized official model label %q", cells[0])
		}
		if _, ok := tables[active][name]; ok {
			return Catalog{}, errors.New("duplicate official model price")
		}
		short, err := parseRates(cells[1:5])
		if err != nil || short == nil {
			return Catalog{}, fmt.Errorf("invalid %s short-context price for %s", active, name)
		}
		long, err := parseRates(cells[5:])
		if err != nil {
			return Catalog{}, err
		}
		tables[active][name] = TierRates{Short: *short, Long: long}
		rowSeen = true
	}
	if err := scanner.Err(); err != nil {
		return Catalog{}, err
	}
	for _, tier := range []string{"default", "fast", "ultrafast"} {
		if len(tables[tier]) == 0 {
			return Catalog{}, fmt.Errorf("official %s price table is empty", tier)
		}
	}
	digest := sha256.Sum256([]byte(document))
	c := Catalog{Source: PricingURL, SHA256: hex.EncodeToString(digest[:]), FetchedAt: now, ShortContextLimit: limit, Models: map[string]ModelRates{}}
	for _, tier := range []string{"fast", "ultrafast"} {
		for name, tr := range tables[tier] {
			std, ok := tables["default"][name]
			if !ok {
				return Catalog{}, fmt.Errorf("missing standard fallback price for %s", name)
			}
			entry := c.Models[name]
			entry.Standard = std
			copy := tr
			if tier == "fast" {
				entry.Fast = &copy
			} else {
				entry.Ultrafast = &copy
			}
			c.Models[name] = entry
		}
	}
	return c, c.Validate()
}
