package model

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const storeMediaTestSVG = `<svg viewBox="0 0 48 48"><defs><linearGradient id="color"><stop offset="0" stop-color="#24c"/><stop offset="1" stop-color="#7cf"/></linearGradient></defs><rect width="48" height="48" rx="8" fill="url(#color)"/><text x="8" y="30" font-size="12">店铺</text></svg>`

func TestMerchantStoreMediaStaticSVGAndLegacyImages(t *testing.T) {
	image, err := normalizeMerchantStoreImage(storeMediaTestSVG)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(image, MerchantStoreSVGDataPrefix))
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, MerchantStoreSVGDataPrefix))
	require.NoError(t, err)
	require.Contains(t, string(decoded), `xmlns="http://www.w3.org/2000/svg"`)
	require.Contains(t, string(decoded), "店铺")
	again, err := normalizeMerchantStoreImage(image)
	require.NoError(t, err)
	require.Equal(t, image, again)
	for _, url := range []string{"https://cdn.example.test/a.png?key=public", "http://legacy.example.test/a.jpg"} {
		actual, err := normalizeMerchantStoreImage(url)
		require.NoError(t, err)
		require.Equal(t, url, actual)
	}
	// A comment must not redirect insertion of the root namespace.
	image, err = normalizeMerchantStoreImage(`<!-- <svg is not the root --> <svg><circle r="5"/></svg>`)
	require.NoError(t, err)
	_, err = normalizeMerchantStoreImage(image)
	require.NoError(t, err)
}

func TestMerchantStoreMediaRejectsActiveExternalAndOversizedSVG(t *testing.T) {
	for name, source := range map[string]string{
		"script":            `<svg><script>alert(1)</script></svg>`,
		"event":             `<svg onload="alert(1)"/>`,
		"nested event":      `<svg><path d="M0 0" onclick="alert(1)"/></svg>`,
		"foreign HTML":      `<svg><foreignObject><div>HTML</div></foreignObject></svg>`,
		"image":             `<svg><image href="https://evil.example/track"/></svg>`,
		"external paint":    `<svg><rect fill="url(https://evil.example/x.svg#p)"/></svg>`,
		"encoded paint":     `<svg><rect fill="u&#114;l(https://evil.example/x)"/></svg>`,
		"CSS escape":        `<svg><rect fill="u\72l(https://evil.example/x)"/></svg>`,
		"CSS comment":       `<svg><rect fill="u/**/rl(//evil.example/x)"/></svg>`,
		"style":             `<svg><style>@import url(https://evil.example/x)</style></svg>`,
		"style attr":        `<svg style="background:url(//evil.example/x)"/>`,
		"base":              `<svg xml:base="https://evil.example/"/>`,
		"use":               `<svg><use href="#loop"/></svg>`,
		"animate":           `<svg><animate attributeName="href" to="evil"/></svg>`,
		"entity":            `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg>&x;</svg>`,
		"PI":                `<?xml-stylesheet href="https://evil.example/x"?><svg/>`,
		"other namespace":   `<svg xmlns="https://evil.example/"/>`,
		"namespaced attr":   `<svg xmlns:x="http://www.w3.org/1999/xlink" x:href="evil"/>`,
		"duplicate attr":    `<svg width="24" width="99999"/>`,
		"two roots":         `<svg/><svg/>`,
		"text outside":      `text<svg/>`,
		"malformed":         `<svg><path></svg>`,
		"infinite":          `<svg width="NaN"/>`,
		"large dimension":   `<svg width="4097"/>`,
		"large viewBox":     `<svg viewBox="0 0 9000 9000"/>`,
		"depth":             `<svg>` + strings.Repeat(`<g>`, 32) + strings.Repeat(`</g>`, 32) + `</svg>`,
		"nodes":             `<svg>` + strings.Repeat(`<path/>`, 4096) + `</svg>`,
		"bytes":             `<svg><desc>` + strings.Repeat("x", MerchantStoreSVGMaxBytes) + `</desc></svg>`,
		"URI type":          `data:image/svg+xml,<svg/>`,
		"other URI":         `data:text/html;base64,PHN2Zy8+`,
		"base64 whitespace": MerchantStoreSVGDataPrefix + "PHN2\nZy8+",
		"invalid UTF8":      MerchantStoreSVGDataPrefix + base64.StdEncoding.EncodeToString([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeMerchantStoreImage(source)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			if strings.HasPrefix(source, "<") {
				_, err = normalizeMerchantStoreImage(MerchantStoreSVGDataPrefix + base64.StdEncoding.EncodeToString([]byte(source)))
				require.ErrorIs(t, err, ErrMerchantStoreInput)
			}
		})
	}
}

func TestMerchantStoreMediaSavePreservesGalleryStockAndGuestVisibility(t *testing.T) {
	f := newStoreFixture(t, "balance")
	var stocks []MerchantStoreStock
	require.NoError(t, DB.Order("id").Find(&stocks).Error)
	input := MerchantStoreProductInput{Title: f.product.Title, PriceQuota: f.product.PriceQuota, PaymentMethods: []string{"balance"}, ImageURLs: []string{storeMediaTestSVG, "https://cdn.example.test/existing.png", "http://legacy.example.test/original.jpg"}}
	p, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, input)
	require.NoError(t, err)
	require.Len(t, p.ImageURLs, 3)
	require.True(t, strings.HasPrefix(p.ImageURLs[0], MerchantStoreSVGDataPrefix))
	require.Equal(t, input.ImageURLs[1:], p.ImageURLs[1:])
	require.Equal(t, storeMediaTestSVG, input.ImageURLs[0], "validation must not mutate the caller's gallery")
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, ""))
	public, err := GetMerchantStoreProductForViewer(0, p.ID)
	require.NoError(t, err)
	require.Equal(t, p.ImageURLs, public.ImageURLs)
	list, err := ListMerchantStoreProductsForViewer(0, "", 0, 30)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, p.ImageURLs, list[0].ImageURLs)
	var after []MerchantStoreStock
	require.NoError(t, DB.Order("id").Find(&after).Error)
	require.Equal(t, stocks, after)
	bad := input
	bad.ImageURLs = []string{`<svg onload="evil()"/>`}
	_, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, bad)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	var unchanged MerchantStoreProduct
	require.NoError(t, DB.First(&unchanged, "id = ?", p.ID).Error)
	require.Equal(t, p.ImageURLs, unchanged.ImageURLs)
	require.Equal(t, "published", unchanged.Status)
	// SVG support is image-only, never an exception for ordinary product links.
	bad = input
	bad.Links = []MerchantStoreLink{{Title: "not a link", URL: p.ImageURLs[0]}}
	_, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, bad)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
}

func TestMerchantStoreMediaOptionalLogoHeaderPreserveGalleryPositions(t *testing.T) {
	for _, images := range [][]string{
		{"", storeMediaTestSVG, "https://cdn.example.test/gallery.png"},
		{"https://cdn.example.test/logo.png", "", "https://cdn.example.test/gallery.png"},
	} {
		input := MerchantStoreProductInput{Title: "Independent media roles", PriceQuota: 500000, ImageURLs: images}
		require.NoError(t, validateStoreProduct(&input))
		require.Len(t, input.ImageURLs, 3)
		require.Equal(t, images[0], input.ImageURLs[0])
		require.Equal(t, images[2], input.ImageURLs[2])
		if images[1] == "" {
			require.Empty(t, input.ImageURLs[1])
		} else {
			require.True(t, strings.HasPrefix(input.ImageURLs[1], MerchantStoreSVGDataPrefix))
		}
	}
	input := MerchantStoreProductInput{Title: "Not an optional role", PriceQuota: 500000, ImageURLs: []string{"", "", ""}}
	require.ErrorIs(t, validateStoreProduct(&input), ErrMerchantStoreInput)
}
